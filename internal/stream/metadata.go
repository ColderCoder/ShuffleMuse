package stream

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ColderCoder/ShuffleMuse/internal/mediaexec"
)

const (
	maxProbeOutputBytes   = int64(64 << 10)
	maxMetadataTitleBytes = 512
)

type Metadata struct {
	Title              string  `json:"title,omitempty"`
	Codec              string  `json:"codec"`
	BitrateKbps        int     `json:"bitrateKbps"`
	BitrateApproximate bool    `json:"bitrateApproximate"`
	DurationSeconds    float64 `json:"durationSeconds"`
}

type MetadataConfig struct {
	Capacity    int
	NegativeTTL time.Duration
	TaskTimeout time.Duration
}

type metadataKey struct {
	path        string
	size        int64
	modUnixNano int64
}

type embeddedCoverInfo struct {
	width  int
	height int
}

type probeData struct {
	metadata         Metadata
	metadataErr      error
	embeddedCover    embeddedCoverInfo
	hasEmbeddedCover bool
}

type metadataCacheEntry struct {
	key  metadataKey
	data probeData
}

type metadataNegativeEntry struct {
	key     metadataKey
	err     error
	expires time.Time
}

type metadataFlight struct {
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	waiters   int
	finished  bool
	abandoned bool
	data      probeData
	err       error
}

type MetadataProbe struct {
	mu sync.Mutex

	positive    map[metadataKey]*list.Element
	positiveLRU *list.List
	negative    map[metadataKey]*list.Element
	negativeLRU *list.List
	flights     map[metadataKey]*metadataFlight
	capacity    int
	negativeTTL time.Duration
	taskTimeout time.Duration
	executor    mediaexec.Executor
	now         func() time.Time
	probe       func(context.Context, metadataKey) (probeData, error)
}

func NewMetadataProbe(executor mediaexec.Executor, options ...MetadataConfig) *MetadataProbe {
	config := MetadataConfig{Capacity: 4096, NegativeTTL: 30 * time.Second, TaskTimeout: 15 * time.Second}
	if len(options) > 0 {
		if options[0].Capacity > 0 {
			config.Capacity = options[0].Capacity
		}
		if options[0].NegativeTTL > 0 {
			config.NegativeTTL = options[0].NegativeTTL
		}
		if options[0].TaskTimeout > 0 {
			config.TaskTimeout = options[0].TaskTimeout
		}
	}
	p := &MetadataProbe{
		positive: make(map[metadataKey]*list.Element), positiveLRU: list.New(),
		negative: make(map[metadataKey]*list.Element), negativeLRU: list.New(),
		flights: make(map[metadataKey]*metadataFlight), capacity: config.Capacity,
		negativeTTL: config.NegativeTTL, taskTimeout: config.TaskTimeout,
		executor: executor, now: time.Now,
	}
	p.probe = p.probeCommand
	return p
}

func (p *MetadataProbe) Probe(ctx context.Context, path string) (Metadata, error) {
	data, err := p.inspect(ctx, path)
	if err != nil {
		return Metadata{}, err
	}
	if data.metadataErr != nil {
		return Metadata{}, data.metadataErr
	}
	return data.metadata, nil
}

// ProbeEmbedded shares the same identity cache and in-flight ffprobe as Probe.
// Probe failures mean no usable embedded artwork, while busy, timeout, and
// cancellation errors remain visible to the cover endpoint.
func (p *MetadataProbe) ProbeEmbedded(ctx context.Context, path string) (width, height int, found bool, err error) {
	data, err := p.inspect(ctx, path)
	if err != nil {
		if mediaexec.IsBusy(err) || mediaexec.IsTimeout(err) ||
			errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return 0, 0, false, err
		}
		return 0, 0, false, nil
	}
	if !data.hasEmbeddedCover {
		return 0, 0, false, nil
	}
	return data.embeddedCover.width, data.embeddedCover.height, true, nil
}

func (p *MetadataProbe) inspect(ctx context.Context, path string) (probeData, error) {
	if err := ctx.Err(); err != nil {
		return probeData{}, err
	}
	stat, err := os.Stat(path)
	if err != nil {
		return probeData{}, err
	}
	key := metadataKey{path: path, size: stat.Size(), modUnixNano: stat.ModTime().UnixNano()}
	now := p.now()

	p.mu.Lock()
	if element := p.positive[key]; element != nil {
		p.positiveLRU.MoveToFront(element)
		data := element.Value.(metadataCacheEntry).data
		p.mu.Unlock()
		return data, nil
	}
	if element := p.negative[key]; element != nil {
		cached := element.Value.(metadataNegativeEntry)
		if now.Before(cached.expires) {
			p.negativeLRU.MoveToFront(element)
			p.mu.Unlock()
			return probeData{}, cached.err
		}
		p.negativeLRU.Remove(element)
		delete(p.negative, key)
	}
	flight := p.flights[key]
	if flight == nil {
		flightCtx, cancel := context.WithCancel(context.Background())
		flight = &metadataFlight{ctx: flightCtx, cancel: cancel, done: make(chan struct{}), waiters: 1}
		p.flights[key] = flight
		go p.runFlight(key, flight)
	} else {
		flight.waiters++
	}
	p.mu.Unlock()

	select {
	case <-flight.done:
		return flight.data, flight.err
	case <-ctx.Done():
		p.leaveFlight(key, flight)
		return probeData{}, ctx.Err()
	}
}

func (p *MetadataProbe) leaveFlight(key metadataKey, flight *metadataFlight) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if flight.finished || flight.waiters <= 0 {
		return
	}
	flight.waiters--
	if flight.waiters == 0 {
		flight.abandoned = true
		if p.flights[key] == flight {
			delete(p.flights, key)
		}
		flight.cancel()
	}
}

func (p *MetadataProbe) runFlight(key metadataKey, flight *metadataFlight) {
	data, err := p.probe(flight.ctx, key)
	if errors.Is(flight.ctx.Err(), context.DeadlineExceeded) &&
		(errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
		err = fmt.Errorf("metadata deadline: %w", mediaexec.ErrTaskTimeout)
	}

	p.mu.Lock()
	flight.data, flight.err, flight.finished = data, err, true
	if p.flights[key] == flight {
		delete(p.flights, key)
	}
	if !flight.abandoned {
		if err == nil {
			p.addPositiveLocked(key, data)
		} else if isDeterministicMetadataError(err) {
			p.addNegativeLocked(key, err, p.now().Add(p.negativeTTL))
		}
	}
	close(flight.done)
	p.mu.Unlock()
	flight.cancel()
}

func (p *MetadataProbe) addPositiveLocked(key metadataKey, data probeData) {
	if existing := p.positive[key]; existing != nil {
		p.positiveLRU.Remove(existing)
	}
	element := p.positiveLRU.PushFront(metadataCacheEntry{key: key, data: data})
	p.positive[key] = element
	for p.positiveLRU.Len() > p.capacity {
		oldest := p.positiveLRU.Back()
		delete(p.positive, oldest.Value.(metadataCacheEntry).key)
		p.positiveLRU.Remove(oldest)
	}
}

func (p *MetadataProbe) addNegativeLocked(key metadataKey, err error, expires time.Time) {
	if existing := p.negative[key]; existing != nil {
		p.negativeLRU.Remove(existing)
	}
	element := p.negativeLRU.PushFront(metadataNegativeEntry{key: key, err: err, expires: expires})
	p.negative[key] = element
	for p.negativeLRU.Len() > p.capacity {
		oldest := p.negativeLRU.Back()
		delete(p.negative, oldest.Value.(metadataNegativeEntry).key)
		p.negativeLRU.Remove(oldest)
	}
}

type deterministicMetadataError struct{ error }

func isDeterministicMetadataError(err error) bool {
	var deterministic deterministicMetadataError
	return errors.As(err, &deterministic)
}

func (p *MetadataProbe) probeCommand(ctx context.Context, key metadataKey) (probeData, error) {
	task, err := mediaexec.Start(p.executor, ctx, mediaexec.AuxHigh)
	if err != nil {
		return probeData{}, fmt.Errorf("wait for metadata lane: %w", err)
	}
	defer task.Done()
	commandCtx, cancel := context.WithTimeout(task.Context(), p.taskTimeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx,
		"ffprobe",
		"-v", "error",
		"-show_entries",
		"stream=codec_type,codec_name,bit_rate,duration,width,height:stream_tags=title:"+
			"format=bit_rate,duration:format_tags=title",
		"-of", "json",
		key.path,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return probeData{}, fmt.Errorf("open ffprobe output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return probeData{}, fmt.Errorf("start ffprobe %s: %w", filepath.Base(key.path), err)
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, maxProbeOutputBytes+1))
	outputTooLarge := int64(len(output)) > maxProbeOutputBytes
	if readErr != nil || outputTooLarge {
		cancel()
	}
	waitErr := cmd.Wait()
	if readErr != nil || waitErr != nil || outputTooLarge {
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return probeData{}, fmt.Errorf("metadata deadline: %w", mediaexec.ErrTaskTimeout)
		}
		if contextErr := task.Context().Err(); contextErr != nil {
			return probeData{}, contextErr
		}
		if outputTooLarge {
			return probeData{}, deterministicMetadataError{
				fmt.Errorf("ffprobe output for %s exceeds %d bytes", filepath.Base(key.path), maxProbeOutputBytes),
			}
		}
		if readErr != nil {
			return probeData{}, fmt.Errorf("read ffprobe output for %s: %w", filepath.Base(key.path), readErr)
		}
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			return probeData{}, deterministicMetadataError{fmt.Errorf("ffprobe %s: %w", filepath.Base(key.path), waitErr)}
		}
		return probeData{}, fmt.Errorf("ffprobe %s: %w", filepath.Base(key.path), waitErr)
	}

	var probe ffprobeOutput
	if err := json.Unmarshal(output, &probe); err != nil {
		return probeData{}, deterministicMetadataError{fmt.Errorf("parse ffprobe output: %w", err)}
	}
	metadata, err := metadataFromProbe(probe, key.path, key.size)
	data := probeData{metadata: metadata}
	if err != nil {
		data.metadataErr = deterministicMetadataError{err}
	}
	if video := firstStream(probe.Streams, "video"); video != nil {
		data.embeddedCover = embeddedCoverInfo{width: video.Width, height: video.Height}
		data.hasEmbeddedCover = true
	}
	return data, nil
}

type ffprobeTags struct {
	Title string `json:"title"`
}

type ffprobeStream struct {
	CodecType string      `json:"codec_type"`
	CodecName string      `json:"codec_name"`
	BitRate   string      `json:"bit_rate"`
	Duration  string      `json:"duration"`
	Width     int         `json:"width"`
	Height    int         `json:"height"`
	Tags      ffprobeTags `json:"tags"`
}

type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  struct {
		BitRate  string      `json:"bit_rate"`
		Duration string      `json:"duration"`
		Tags     ffprobeTags `json:"tags"`
	} `json:"format"`
}

func metadataFromProbe(probe ffprobeOutput, path string, size int64) (Metadata, error) {
	metadata := Metadata{
		Codec: strings.ToUpper(strings.TrimPrefix(filepath.Ext(path), ".")),
		Title: normalizeMetadataTitle(probe.Format.Tags.Title),
	}
	var streamBitrate string
	var streamDuration string
	if audio := firstStream(probe.Streams, "audio"); audio != nil {
		if audio.CodecName != "" {
			metadata.Codec = strings.ToUpper(audio.CodecName)
		}
		if metadata.Title == "" {
			metadata.Title = normalizeMetadataTitle(audio.Tags.Title)
		}
		streamBitrate = audio.BitRate
		streamDuration = audio.Duration
	}

	metadata.DurationSeconds = firstPositiveFloat(streamDuration, probe.Format.Duration)
	if metadata.DurationSeconds <= 0 {
		return metadata, fmt.Errorf("audio duration is unavailable")
	}

	bitrate := firstPositiveFloat(streamBitrate, probe.Format.BitRate)
	if bitrate <= 0 && size > 0 {
		bitrate = float64(size*8) / metadata.DurationSeconds
		metadata.BitrateApproximate = true
	}
	if bitrate > 0 {
		metadata.BitrateKbps = int(math.Round(bitrate / 1000))
	}
	return metadata, nil
}

func firstStream(streams []ffprobeStream, codecType string) *ffprobeStream {
	for i := range streams {
		if streams[i].CodecType == codecType {
			return &streams[i]
		}
	}
	return nil
}

func normalizeMetadataTitle(title string) string {
	title = strings.TrimSpace(title)
	if len(title) <= maxMetadataTitleBytes {
		return title
	}
	cut := maxMetadataTitleBytes
	for cut > 0 && !utf8.RuneStart(title[cut]) {
		cut--
	}
	return title[:cut]
}

func firstPositiveFloat(values ...string) float64 {
	for _, value := range values {
		parsed, err := strconv.ParseFloat(value, 64)
		if err == nil && parsed > 0 && !math.IsInf(parsed, 0) && !math.IsNaN(parsed) {
			return parsed
		}
	}
	return 0
}
