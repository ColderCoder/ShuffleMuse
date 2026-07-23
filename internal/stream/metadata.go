package stream

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/mediaexec"
)

type Metadata struct {
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

type metadataCacheEntry struct {
	key      metadataKey
	metadata Metadata
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
	metadata  Metadata
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
	probe       func(context.Context, metadataKey) (Metadata, error)
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
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	stat, err := os.Stat(path)
	if err != nil {
		return Metadata{}, err
	}
	key := metadataKey{path: path, size: stat.Size(), modUnixNano: stat.ModTime().UnixNano()}
	now := p.now()

	p.mu.Lock()
	if element := p.positive[key]; element != nil {
		p.positiveLRU.MoveToFront(element)
		metadata := element.Value.(metadataCacheEntry).metadata
		p.mu.Unlock()
		return metadata, nil
	}
	if element := p.negative[key]; element != nil {
		cached := element.Value.(metadataNegativeEntry)
		if now.Before(cached.expires) {
			p.negativeLRU.MoveToFront(element)
			p.mu.Unlock()
			return Metadata{}, cached.err
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
		return flight.metadata, flight.err
	case <-ctx.Done():
		p.leaveFlight(key, flight)
		return Metadata{}, ctx.Err()
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
	metadata, err := p.probe(flight.ctx, key)
	if errors.Is(flight.ctx.Err(), context.DeadlineExceeded) &&
		(errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
		err = fmt.Errorf("metadata deadline: %w", mediaexec.ErrTaskTimeout)
	}

	p.mu.Lock()
	flight.metadata, flight.err, flight.finished = metadata, err, true
	if p.flights[key] == flight {
		delete(p.flights, key)
	}
	if !flight.abandoned {
		if err == nil {
			p.addPositiveLocked(key, metadata)
		} else if isDeterministicMetadataError(err) {
			p.addNegativeLocked(key, err, p.now().Add(p.negativeTTL))
		}
	}
	close(flight.done)
	p.mu.Unlock()
	flight.cancel()
}

func (p *MetadataProbe) addPositiveLocked(key metadataKey, metadata Metadata) {
	if existing := p.positive[key]; existing != nil {
		p.positiveLRU.Remove(existing)
	}
	element := p.positiveLRU.PushFront(metadataCacheEntry{key: key, metadata: metadata})
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

func (p *MetadataProbe) probeCommand(ctx context.Context, key metadataKey) (Metadata, error) {
	task, err := mediaexec.Start(p.executor, ctx, mediaexec.AuxHigh)
	if err != nil {
		return Metadata{}, fmt.Errorf("wait for metadata lane: %w", err)
	}
	defer task.Done()
	commandCtx, cancel := context.WithTimeout(task.Context(), p.taskTimeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx,
		"ffprobe",
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=codec_name,bit_rate,duration:format=bit_rate,duration",
		"-of", "json",
		key.path,
	)
	output, err := cmd.Output()
	if err != nil {
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return Metadata{}, fmt.Errorf("metadata deadline: %w", mediaexec.ErrTaskTimeout)
		}
		if contextErr := task.Context().Err(); contextErr != nil {
			return Metadata{}, contextErr
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return Metadata{}, deterministicMetadataError{fmt.Errorf("ffprobe %s: %w", filepath.Base(key.path), err)}
		}
		return Metadata{}, fmt.Errorf("ffprobe %s: %w", filepath.Base(key.path), err)
	}

	var probe ffprobeOutput
	if err := json.Unmarshal(output, &probe); err != nil {
		return Metadata{}, deterministicMetadataError{fmt.Errorf("parse ffprobe output: %w", err)}
	}
	metadata, err := metadataFromProbe(probe, key.path, key.size)
	if err != nil {
		return Metadata{}, deterministicMetadataError{err}
	}
	return metadata, nil
}

type ffprobeOutput struct {
	Streams []struct {
		CodecName string `json:"codec_name"`
		BitRate   string `json:"bit_rate"`
		Duration  string `json:"duration"`
	} `json:"streams"`
	Format struct {
		BitRate  string `json:"bit_rate"`
		Duration string `json:"duration"`
	} `json:"format"`
}

func metadataFromProbe(probe ffprobeOutput, path string, size int64) (Metadata, error) {
	metadata := Metadata{Codec: strings.ToUpper(strings.TrimPrefix(filepath.Ext(path), "."))}
	var streamBitrate string
	var streamDuration string
	if len(probe.Streams) > 0 {
		if probe.Streams[0].CodecName != "" {
			metadata.Codec = strings.ToUpper(probe.Streams[0].CodecName)
		}
		streamBitrate = probe.Streams[0].BitRate
		streamDuration = probe.Streams[0].Duration
	}

	metadata.DurationSeconds = firstPositiveFloat(streamDuration, probe.Format.Duration)
	if metadata.DurationSeconds <= 0 {
		return Metadata{}, fmt.Errorf("audio duration is unavailable")
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

func firstPositiveFloat(values ...string) float64 {
	for _, value := range values {
		parsed, err := strconv.ParseFloat(value, 64)
		if err == nil && parsed > 0 && !math.IsInf(parsed, 0) && !math.IsNaN(parsed) {
			return parsed
		}
	}
	return 0
}
