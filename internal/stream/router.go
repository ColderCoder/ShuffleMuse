package stream

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/mediaexec"
)

var ErrInvalidStreamOptions = errors.New("invalid stream options")

type Router struct {
	Bitrate        int
	Media          mediaexec.Executor
	Limiter        *mediaexec.Limiter // legacy shared-mode adapter
	StartupTimeout time.Duration
	WriteIdle      time.Duration
}

func (r *Router) ServeStream(w http.ResponseWriter, req *http.Request, filepath string) error {
	mode := req.URL.Query().Get("mode")
	startValue := req.URL.Query().Get("start")

	switch mode {
	case "":
		if startValue != "" {
			return fmt.Errorf("%w: start requires mode=opus", ErrInvalidStreamOptions)
		}
		if IsNativeOpus(filepath) {
			return ServeFile(w, req, filepath)
		}
		if req.Method == http.MethodHead {
			setTranscodeHeaders(w)
			w.WriteHeader(http.StatusOK)
			return nil
		}
		return TranscodeWithConfig(w, req, filepath, r.Bitrate, r.executor(), TranscodeConfig{StartupTimeout: r.StartupTimeout, WriteIdle: r.WriteIdle})
	case "original":
		if startValue != "" {
			return fmt.Errorf("%w: original mode uses HTTP Range", ErrInvalidStreamOptions)
		}
		return ServeFile(w, req, filepath)
	case "opus":
		start, err := parseStartSeconds(startValue)
		if err != nil {
			return err
		}
		if req.Method == http.MethodHead {
			setTranscodeHeaders(w)
			w.WriteHeader(http.StatusOK)
			return nil
		}
		return TranscodeAtWithConfig(w, req, filepath, r.Bitrate, start, r.executor(), TranscodeConfig{StartupTimeout: r.StartupTimeout, WriteIdle: r.WriteIdle})
	default:
		return fmt.Errorf("%w: unsupported mode %q", ErrInvalidStreamOptions, mode)
	}
}

func (r *Router) executor() mediaexec.Executor {
	if r.Media != nil {
		return r.Media
	}
	return r.Limiter
}

func parseStartSeconds(value string) (float64, error) {
	if value == "" {
		return 0, nil
	}
	start, err := strconv.ParseFloat(value, 64)
	if err != nil || start < 0 || math.IsNaN(start) || math.IsInf(start, 0) {
		return 0, fmt.Errorf("%w: start must be a non-negative number", ErrInvalidStreamOptions)
	}
	return start, nil
}
