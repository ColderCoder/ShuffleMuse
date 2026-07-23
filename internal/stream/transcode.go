package stream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/mediaexec"
)

const maxTranscodeStderr = 64 << 10

type TranscodeConfig struct {
	StartupTimeout time.Duration
	WriteIdle      time.Duration
}

var transcodeWg sync.WaitGroup

// WaitTranscodes is retained for embedders using no Manager. The production
// server drains the Manager, which covers transcodes and auxiliary tasks.
func WaitTranscodes(ctx context.Context) bool {
	done := make(chan struct{})
	go func() {
		transcodeWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func Transcode(w http.ResponseWriter, r *http.Request, filepath string, bitrate int, executor mediaexec.Executor) error {
	return TranscodeWithConfig(w, r, filepath, bitrate, executor, TranscodeConfig{})
}

func TranscodeAt(w http.ResponseWriter, r *http.Request, filepath string, bitrate int, startSeconds float64, executor mediaexec.Executor) error {
	return TranscodeAtWithConfig(w, r, filepath, bitrate, startSeconds, executor, TranscodeConfig{})
}

func TranscodeWithConfig(w http.ResponseWriter, r *http.Request, filepath string, bitrate int, executor mediaexec.Executor, config TranscodeConfig) error {
	return TranscodeAtWithConfig(w, r, filepath, bitrate, 0, executor, config)
}

func TranscodeAtWithConfig(w http.ResponseWriter, r *http.Request, filepath string, bitrate int, startSeconds float64, executor mediaexec.Executor, config TranscodeConfig) error {
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = 15 * time.Second
	}
	if config.WriteIdle <= 0 {
		config.WriteIdle = 60 * time.Second
	}
	transcodeWg.Add(1)
	defer transcodeWg.Done()
	task, err := mediaexec.Start(executor, r.Context(), mediaexec.Transcode)
	if err != nil {
		return fmt.Errorf("wait for transcode lane: %w", err)
	}
	defer task.Done()

	setTranscodeHeaders(w)
	processCtx, cancelProcess := context.WithCancel(task.Context())
	defer cancelProcess()
	bitrateStr := strconv.Itoa(bitrate) + "k"
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if startSeconds > 0 {
		args = append(args, "-ss", strconv.FormatFloat(startSeconds, 'f', 3, 64))
	}
	args = append(args,
		"-i", filepath,
		"-vn", "-map", "0:a:0", "-c:a", "libopus", "-b:a", bitrateStr,
		"-f", "ogg", "-vbr", "on", "-reset_timestamps", "1", "-",
	)
	cmd := exec.CommandContext(processCtx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}
	stderr := &limitedBuffer{maximum: maxTranscodeStderr}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg start: %w", err)
	}

	firstWrite := make(chan struct{})
	processDone := make(chan struct{})
	var firstOnce sync.Once
	var startupTimedOut atomic.Bool
	startupDeadline := time.Now().Add(config.StartupTimeout)
	timer := time.NewTimer(config.StartupTimeout)
	go func() {
		select {
		case <-timer.C:
			startupTimedOut.Store(true)
			cancelProcess()
		case <-firstWrite:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-processDone:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
	}()

	controller := http.NewResponseController(w)
	buffer := make([]byte, 32<<10)
	committed := false
	var copyErr error
	for {
		n, readErr := stdout.Read(buffer)
		if n > 0 {
			deadline := startupDeadline
			if committed {
				deadline = time.Now().Add(config.WriteIdle)
			}
			_ = controller.SetWriteDeadline(deadline)
			written, writeErr := w.Write(buffer[:n])
			if written > 0 && !committed {
				committed = true
				firstOnce.Do(func() { close(firstWrite) })
				_ = controller.Flush()
			}
			if writeErr != nil {
				copyErr = fmt.Errorf("write transcoded stream: %w", writeErr)
				break
			}
			if written != n {
				copyErr = io.ErrShortWrite
				break
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				copyErr = fmt.Errorf("read ffmpeg output: %w", readErr)
			}
			break
		}
	}
	if copyErr != nil || r.Context().Err() != nil {
		// Terminate first; only then Wait so a blocked media process cannot hold
		// its lane after the client has gone away.
		cancelProcess()
	}
	waitErr := cmd.Wait()
	close(processDone)
	_ = controller.SetWriteDeadline(time.Time{})

	if startupTimedOut.Load() && !committed {
		return fmt.Errorf("ffmpeg first byte: %w", mediaexec.ErrTaskTimeout)
	}
	if copyErr != nil {
		return copyErr
	}
	if requestErr := r.Context().Err(); requestErr != nil {
		return requestErr
	}
	if waitErr != nil {
		if task.Context().Err() != nil {
			return task.Context().Err()
		}
		log.Printf("ffmpeg exited with error: %v, stderr: %s", waitErr, stderr.String())
		return fmt.Errorf("ffmpeg process error: %w", waitErr)
	}
	return nil
}

func setTranscodeHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "audio/ogg; codecs=opus")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
}

type limitedBuffer struct {
	buffer  bytes.Buffer
	maximum int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.maximum - b.buffer.Len()
	if remaining > 0 {
		_, _ = b.buffer.Write(p[:min(remaining, len(p))])
	}
	return original, nil
}

func (b *limitedBuffer) String() string { return b.buffer.String() }
