package cover

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/mediaexec"
)

const (
	maxCoverBytes       = int64(20 << 20)
	maxCoverDimension   = 8192
	maxCoverPixels      = int64(40_000_000)
	compressCoverBytes  = int64(1 << 20)
	compressCoverEdge   = 1536
	renderCoverEdge     = 1024
	jpegQuality         = 3
	jpegKeepPercent     = int64(85)
	maxStderrBytes      = 64 << 10
	coverEncodingSpec   = "mjpeg-q3-max1024-no-upscale-white-v1"
	coverThresholdsSpec = "safe:20MiB,8192,40MP;compress:1MiB,1536;keep:85pct"
)

var (
	ErrNotFound = errors.New("cover art not found")
	ErrStale    = errors.New("cover descriptor is stale")
)

type Kind string

const (
	Embedded Kind = "embedded"
	Fallback Kind = "fallback"
)

// Descriptor contains only small, source-derived metadata. Image bytes are
// deliberately never retained in the descriptor cache.
type Descriptor struct {
	Kind                Kind
	ContentType         string
	OriginalContentType string
	Name                string
	Source              string
	ModTime             time.Time
	ETag                string
	Size                int64
	FilePath            string
	Width               int
	Height              int
	RequiresRender      bool

	audioPath    string
	audioSize    int64
	audioModNano int64
	fileSize     int64
	fileModNano  int64
}

// Result remains useful to callers that want an in-memory image.
type Result struct {
	Data        []byte
	ContentType string
	Name        string
	Source      string
	ModTime     time.Time
}

type Config struct {
	Entries     int
	Bytes       int64
	NegativeTTL time.Duration
	TaskTimeout time.Duration
}

type descriptorKind uint8

const (
	directoryDescriptor descriptorKind = iota
	embeddedDescriptor
)

type descriptorKey struct {
	kind        descriptorKind
	path        string
	size        int64
	modUnixNano int64
}

type descriptorCacheEntry struct {
	key        descriptorKey
	descriptor Descriptor
	err        error
	expires    time.Time
	bytes      int64
	used       uint64
}

type descriptorFlight struct {
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	waiters    int
	finished   bool
	abandoned  bool
	descriptor Descriptor
	err        error
}

type renderFlight struct {
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	waiters   int
	finished  bool
	abandoned bool
	data      []byte
	err       error
}

type Loader struct {
	mu sync.Mutex

	descriptors   map[descriptorKey]*list.Element
	descriptorLRU *list.List
	describeCalls map[descriptorKey]*descriptorFlight
	renderCalls   map[string]*renderFlight
	totalBytes    int64
	clock         uint64
	maxEntries    int
	maxBytes      int64
	negativeTTL   time.Duration
	taskTimeout   time.Duration
	executor      mediaexec.Executor
	now           func() time.Time
	discover      func(context.Context, descriptorKey) (Descriptor, error)
	transcode     func(context.Context, Descriptor) ([]byte, error)
}

func NewLoader(executor mediaexec.Executor, options ...Config) *Loader {
	config := Config{Entries: 128, Bytes: 64 << 20, NegativeTTL: 30 * time.Second, TaskTimeout: 15 * time.Second}
	if len(options) > 0 {
		if options[0].Entries > 0 {
			config.Entries = options[0].Entries
		}
		if options[0].Bytes > 0 {
			config.Bytes = options[0].Bytes
		}
		if options[0].NegativeTTL > 0 {
			config.NegativeTTL = options[0].NegativeTTL
		}
		if options[0].TaskTimeout > 0 {
			config.TaskTimeout = options[0].TaskTimeout
		}
	}
	loader := &Loader{
		descriptors: make(map[descriptorKey]*list.Element), descriptorLRU: list.New(),
		describeCalls: make(map[descriptorKey]*descriptorFlight), renderCalls: make(map[string]*renderFlight),
		maxEntries: config.Entries, maxBytes: config.Bytes,
		negativeTTL: config.NegativeTTL, taskTimeout: config.TaskTimeout,
		executor: executor, now: time.Now,
	}
	loader.discover = loader.discoverDescriptor
	loader.transcode = loader.transcodeJPEG
	return loader
}

// DescribeDirectory discovers only a cover.jpg or cover.png in directory. It
// never probes audio files, so all tracks in the directory receive the same
// descriptor and ETag.
func (l *Loader) DescribeDirectory(ctx context.Context, directory string) (Descriptor, error) {
	if err := ctx.Err(); err != nil {
		return Descriptor{}, err
	}
	info, err := os.Lstat(directory)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return Descriptor{}, ErrNotFound
	}
	key := descriptorKey{kind: directoryDescriptor, path: directory, modUnixNano: info.ModTime().UnixNano()}
	return l.describe(ctx, key)
}

// Describe gives an external directory cover priority. ffprobe is only used
// when no eligible directory cover exists.
func (l *Loader) Describe(ctx context.Context, audioPath string) (Descriptor, error) {
	if err := ctx.Err(); err != nil {
		return Descriptor{}, err
	}
	info, err := os.Stat(audioPath)
	if err != nil || !info.Mode().IsRegular() {
		return Descriptor{}, ErrNotFound
	}

	descriptor, directoryErr := l.DescribeDirectory(ctx, filepath.Dir(audioPath))
	if directoryErr == nil {
		return descriptor, nil
	}
	// Only the exact absence sentinel permits embedded fallback. An existing
	// but invalid/unsafe directory cover remains a stable COVER_NOT_FOUND.
	if directoryErr != ErrNotFound {
		return Descriptor{}, directoryErr
	}

	key := descriptorKey{
		kind: embeddedDescriptor, path: audioPath,
		size: info.Size(), modUnixNano: info.ModTime().UnixNano(),
	}
	return l.describe(ctx, key)
}

func (l *Loader) describe(ctx context.Context, key descriptorKey) (Descriptor, error) {
	now := l.now()
	l.mu.Lock()
	if element := l.descriptors[key]; element != nil {
		entry := element.Value.(descriptorCacheEntry)
		if entry.err != nil && !now.Before(entry.expires) {
			l.removeDescriptorLocked(element)
		} else if entry.err == nil && !descriptorStillValid(entry.descriptor) {
			l.removeDescriptorLocked(element)
		} else {
			l.touchDescriptorLocked(element)
			l.mu.Unlock()
			return entry.descriptor, entry.err
		}
	}
	flight := l.describeCalls[key]
	if flight == nil {
		flightCtx, cancel := context.WithCancel(context.Background())
		flight = &descriptorFlight{ctx: flightCtx, cancel: cancel, done: make(chan struct{}), waiters: 1}
		l.describeCalls[key] = flight
		go l.runDescribe(key, flight)
	} else {
		flight.waiters++
	}
	l.mu.Unlock()

	select {
	case <-flight.done:
		return flight.descriptor, flight.err
	case <-ctx.Done():
		l.leaveDescribe(key, flight)
		return Descriptor{}, ctx.Err()
	}
}

func (l *Loader) runDescribe(key descriptorKey, flight *descriptorFlight) {
	descriptor, err := l.discover(flight.ctx, key)
	l.mu.Lock()
	flight.descriptor, flight.err, flight.finished = descriptor, err, true
	if l.describeCalls[key] == flight {
		delete(l.describeCalls, key)
	}
	if !flight.abandoned && (err == nil || errors.Is(err, ErrNotFound)) {
		expires := time.Time{}
		if err != nil {
			expires = l.now().Add(l.negativeTTL)
		}
		l.addDescriptorLocked(key, descriptor, err, expires)
	}
	close(flight.done)
	l.mu.Unlock()
	flight.cancel()
}

func (l *Loader) leaveDescribe(key descriptorKey, flight *descriptorFlight) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if flight.finished || flight.waiters <= 0 {
		return
	}
	flight.waiters--
	if flight.waiters == 0 {
		flight.abandoned = true
		if l.describeCalls[key] == flight {
			delete(l.describeCalls, key)
		}
		flight.cancel()
	}
}

// Render performs request-local conversion. Concurrent identical conversions
// share only the currently running work; completed image bytes are never put in
// an LRU or retained by Loader.
func (l *Loader) Render(ctx context.Context, descriptor Descriptor) ([]byte, error) {
	if !descriptor.RequiresRender || (descriptor.Kind != Embedded && descriptor.Kind != Fallback) {
		return nil, ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := descriptor.ETag
	l.mu.Lock()
	flight := l.renderCalls[key]
	if flight == nil {
		flightCtx, cancel := context.WithCancel(context.Background())
		flight = &renderFlight{ctx: flightCtx, cancel: cancel, done: make(chan struct{}), waiters: 1}
		l.renderCalls[key] = flight
		go l.runRender(key, descriptor, flight)
	} else {
		flight.waiters++
	}
	l.mu.Unlock()
	select {
	case <-flight.done:
		return flight.data, flight.err
	case <-ctx.Done():
		l.leaveRender(key, flight)
		return nil, ctx.Err()
	}
}

func (l *Loader) runRender(key string, descriptor Descriptor, flight *renderFlight) {
	data, err := l.renderDescriptor(flight.ctx, descriptor)
	l.mu.Lock()
	flight.data, flight.err, flight.finished = data, err, true
	if l.renderCalls[key] == flight {
		delete(l.renderCalls, key)
	}
	close(flight.done)
	l.mu.Unlock()
	flight.cancel()
}

func (l *Loader) leaveRender(key string, flight *renderFlight) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if flight.finished || flight.waiters <= 0 {
		return
	}
	flight.waiters--
	if flight.waiters == 0 {
		flight.abandoned = true
		if l.renderCalls[key] == flight {
			delete(l.renderCalls, key)
		}
		flight.cancel()
	}
}

func (l *Loader) OpenFallback(descriptor Descriptor) (*os.File, error) {
	if descriptor.Kind != Fallback || descriptor.FilePath == "" {
		return nil, ErrNotFound
	}
	before, err := os.Lstat(descriptor.FilePath)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, ErrStale
	}
	file, err := os.Open(descriptor.FilePath)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != descriptor.fileSize || info.ModTime().UnixNano() != descriptor.fileModNano {
		file.Close()
		return nil, ErrStale
	}
	return file, nil
}

func descriptorStillValid(descriptor Descriptor) bool {
	if descriptor.Kind != Fallback {
		return true
	}
	info, err := os.Lstat(descriptor.FilePath)
	return err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() &&
		info.Size() == descriptor.fileSize && info.ModTime().UnixNano() == descriptor.fileModNano
}

func (l *Loader) discoverDescriptor(ctx context.Context, key descriptorKey) (Descriptor, error) {
	switch key.kind {
	case directoryDescriptor:
		return discoverDirectoryCover(key.path)
	case embeddedDescriptor:
		return l.discoverEmbedded(ctx, key)
	default:
		return Descriptor{}, ErrNotFound
	}
}

func discoverDirectoryCover(directory string) (Descriptor, error) {
	coverPath, info, err := findDirectoryCover(directory)
	if err != nil {
		return Descriptor{}, err
	}
	width, height, contentType, err := inspectExternalCover(coverPath, info)
	if err != nil {
		return Descriptor{}, err
	}
	requiresRender := width > compressCoverEdge || height > compressCoverEdge || info.Size() > compressCoverBytes
	name := filepath.Base(coverPath)
	responseType := contentType
	if requiresRender {
		name = "cover.jpg"
		responseType = "image/jpeg"
	}
	return Descriptor{
		Kind: Fallback, ContentType: responseType, OriginalContentType: contentType,
		Name: name, Source: filepath.Base(coverPath), ModTime: info.ModTime(),
		ETag: makeETag(
			"external", coverPath, info.Size(), info.ModTime().UnixNano(), width, height,
			coverThresholdsSpec, coverEncodingSpec, requiresRender,
		),
		Size: info.Size(), FilePath: coverPath, Width: width, Height: height, RequiresRender: requiresRender,
		fileSize: info.Size(), fileModNano: info.ModTime().UnixNano(),
	}, nil
}

func inspectExternalCover(path string, info os.FileInfo) (int, int, string, error) {
	if info.Size() > maxCoverBytes {
		return 0, 0, "", fmt.Errorf("%w: cover %s exceeds %d bytes", ErrNotFound, filepath.Base(path), maxCoverBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, "", err
	}
	defer file.Close()
	config, format, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, "", fmt.Errorf("%w: read cover dimensions: %v", ErrNotFound, err)
	}
	if err := validateDimensions(config.Width, config.Height); err != nil {
		return 0, 0, "", err
	}
	var contentType string
	switch format {
	case "jpeg":
		contentType = "image/jpeg"
	case "png":
		contentType = "image/png"
	default:
		return 0, 0, "", fmt.Errorf("%w: unsupported cover format %q", ErrNotFound, format)
	}
	return config.Width, config.Height, contentType, nil
}

func validateDimensions(width, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("%w: invalid cover dimensions %dx%d", ErrNotFound, width, height)
	}
	if width > maxCoverDimension || height > maxCoverDimension {
		return fmt.Errorf("%w: cover dimensions %dx%d exceed %d on one side", ErrNotFound, width, height, maxCoverDimension)
	}
	if int64(width)*int64(height) > maxCoverPixels {
		return fmt.Errorf("%w: cover dimensions %dx%d exceed %d pixels", ErrNotFound, width, height, maxCoverPixels)
	}
	return nil
}

func (l *Loader) discoverEmbedded(ctx context.Context, key descriptorKey) (Descriptor, error) {
	task, err := mediaexec.Start(l.executor, ctx, mediaexec.AuxHigh)
	if err != nil {
		return Descriptor{}, err
	}
	defer task.Done()
	commandCtx, cancel := context.WithTimeout(task.Context(), l.taskTimeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx,
		"ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=index,width,height", "-of", "json", key.path,
	)
	output, err := cmd.Output()
	if err != nil {
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return Descriptor{}, fmt.Errorf("cover descriptor deadline: %w", mediaexec.ErrTaskTimeout)
		}
		if task.Context().Err() != nil {
			return Descriptor{}, task.Context().Err()
		}
		return Descriptor{}, ErrNotFound
	}
	var result struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &result); err != nil || len(result.Streams) == 0 {
		return Descriptor{}, ErrNotFound
	}
	stream := result.Streams[0]
	if err := validateDimensions(stream.Width, stream.Height); err != nil {
		return Descriptor{}, err
	}
	modTime := time.Unix(0, key.modUnixNano)
	return Descriptor{
		Kind: Embedded, ContentType: "image/jpeg", Name: "cover.jpg", Source: "embedded",
		ModTime: modTime,
		ETag: makeETag(
			"embedded", key.path, key.size, key.modUnixNano, stream.Width, stream.Height,
			coverThresholdsSpec, coverEncodingSpec,
		),
		Width: stream.Width, Height: stream.Height, RequiresRender: true,
		audioPath: key.path, audioSize: key.size, audioModNano: key.modUnixNano,
	}, nil
}

func (l *Loader) renderDescriptor(ctx context.Context, descriptor Descriptor) ([]byte, error) {
	if descriptor.Kind == Embedded {
		info, err := os.Stat(descriptor.audioPath)
		if err != nil || !info.Mode().IsRegular() || info.Size() != descriptor.audioSize || info.ModTime().UnixNano() != descriptor.audioModNano {
			return nil, ErrStale
		}
	} else if descriptor.Kind == Fallback {
		file, err := l.OpenFallback(descriptor)
		if err != nil {
			return nil, err
		}
		file.Close()
	} else {
		return nil, ErrNotFound
	}

	data, err := l.transcode(ctx, descriptor)
	if err != nil {
		return nil, err
	}
	if descriptor.Kind != Fallback || descriptor.OriginalContentType != "image/jpeg" {
		return data, nil
	}
	// Equality is intentional: an output exactly 85% of the source saves the
	// required 15% and is therefore kept.
	if int64(len(data))*100 <= descriptor.fileSize*jpegKeepPercent {
		return data, nil
	}
	return l.readOriginal(descriptor)
}

func (l *Loader) readOriginal(descriptor Descriptor) ([]byte, error) {
	file, err := l.OpenFallback(descriptor)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCoverBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read original cover: %w", err)
	}
	if int64(len(data)) > maxCoverBytes || int64(len(data)) != descriptor.fileSize {
		return nil, ErrStale
	}
	return data, nil
}

func (l *Loader) transcodeJPEG(ctx context.Context, descriptor Descriptor) ([]byte, error) {
	source := descriptor.FilePath
	if descriptor.Kind == Embedded {
		source = descriptor.audioPath
	}
	task, err := mediaexec.Start(l.executor, ctx, mediaexec.AuxRender)
	if err != nil {
		return nil, err
	}
	defer task.Done()
	commandCtx, cancel := context.WithTimeout(task.Context(), l.taskTimeout)
	defer cancel()

	filter := fmt.Sprintf(
		"[0:v:0]scale=w='min(%d,iw)':h='min(%d,ih)':force_original_aspect_ratio=decrease,"+
			"format=rgba,split=2[foreground][background];"+
			"[background]drawbox=x=0:y=0:w=iw:h=ih:color=white@1:t=fill[white];"+
			"[white][foreground]overlay=x=0:y=0:format=auto,format=yuvj420p[cover]",
		renderCoverEdge, renderCoverEdge,
	)
	cmd := exec.CommandContext(commandCtx,
		"ffmpeg", "-v", "error", "-nostdin", "-threads", "1", "-i", source,
		"-filter_complex_threads", "1", "-filter_complex", filter,
		"-map", "[cover]", "-frames:v", "1", "-c:v", "mjpeg", "-q:v", strconv.Itoa(jpegQuality),
		"-threads", "1", "-f", "image2pipe", "pipe:1",
	)
	var stderr cappedBuffer
	stderr.maximum = maxStderrBytes
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open ffmpeg output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, maxCoverBytes+1))
	if readErr != nil || int64(len(data)) > maxCoverBytes {
		task.Cancel()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, fmt.Errorf("read converted cover: %w", readErr)
	}
	if int64(len(data)) > maxCoverBytes {
		return nil, fmt.Errorf("converted cover exceeds %d bytes", maxCoverBytes)
	}
	if waitErr != nil || len(data) == 0 {
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("cover render deadline: %w", mediaexec.ErrTaskTimeout)
		}
		if task.Context().Err() != nil {
			return nil, task.Context().Err()
		}
		if waitErr == nil {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("render cover: %w: %s", waitErr, stderr.String())
	}
	return data, nil
}

func findDirectoryCover(dir string) (string, os.FileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, ErrNotFound
	}
	for _, candidate := range []string{"cover.jpg", "cover.png"} {
		for _, entry := range entries {
			if !strings.EqualFold(entry.Name(), candidate) || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr == nil && info.Mode().IsRegular() {
				return filepath.Join(dir, entry.Name()), info, nil
			}
		}
	}
	return "", nil, ErrNotFound
}

func makeETag(parts ...interface{}) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = io.WriteString(hash, fmt.Sprint(part))
		_, _ = io.WriteString(hash, "\x00")
	}
	return `"` + hex.EncodeToString(hash.Sum(nil)[:16]) + `"`
}

func (l *Loader) addDescriptorLocked(key descriptorKey, descriptor Descriptor, err error, expires time.Time) {
	if existing := l.descriptors[key]; existing != nil {
		l.removeDescriptorLocked(existing)
	}
	l.clock++
	entry := descriptorCacheEntry{key: key, descriptor: descriptor, err: err, expires: expires, used: l.clock}
	entry.bytes = int64(256 + len(key.path) + len(descriptor.ContentType) + len(descriptor.OriginalContentType) +
		len(descriptor.Name) + len(descriptor.Source) + len(descriptor.ETag) + len(descriptor.FilePath))
	element := l.descriptorLRU.PushFront(entry)
	l.descriptors[key] = element
	l.totalBytes += entry.bytes
	l.enforceLimitsLocked()
}

func (l *Loader) touchDescriptorLocked(element *list.Element) {
	l.clock++
	entry := element.Value.(descriptorCacheEntry)
	entry.used = l.clock
	element.Value = entry
	l.descriptorLRU.MoveToFront(element)
}

func (l *Loader) enforceLimitsLocked() {
	for l.descriptorLRU.Len() > l.maxEntries {
		l.removeDescriptorLocked(l.descriptorLRU.Back())
	}
	for l.totalBytes > l.maxBytes && l.descriptorLRU.Len() > 0 {
		l.removeDescriptorLocked(l.descriptorLRU.Back())
	}
}

func (l *Loader) removeDescriptorLocked(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(descriptorCacheEntry)
	delete(l.descriptors, entry.key)
	l.totalBytes -= entry.bytes
	l.descriptorLRU.Remove(element)
}

type cappedBuffer struct {
	buffer  bytes.Buffer
	maximum int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.maximum - b.buffer.Len()
	if remaining > 0 {
		_, _ = b.buffer.Write(p[:min(remaining, len(p))])
	}
	return original, nil
}

func (b *cappedBuffer) String() string { return b.buffer.String() }

// CacheStats reports descriptor metadata only. renders is permanently zero:
// image bytes are never cached after an in-flight conversion completes.
func (l *Loader) CacheStats() (descriptors, renders int, bytes int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.descriptors), 0, l.totalBytes
}

// ContentLength is available without rendering only for an unchanged external
// file. Converted covers deliberately omit Content-Length on HEAD.
func (d Descriptor) ContentLength() string {
	if d.Kind != Fallback || d.RequiresRender || d.Size < 0 {
		return ""
	}
	return strconv.FormatInt(d.Size, 10)
}
