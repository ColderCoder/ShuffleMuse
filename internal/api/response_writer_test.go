package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type basicResponseWriter struct {
	header      http.Header
	body        bytes.Buffer
	statusCodes []int
}

func (w *basicResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *basicResponseWriter) WriteHeader(status int) {
	if len(w.statusCodes) != 0 {
		return
	}
	w.statusCodes = append(w.statusCodes, status)
}

func (w *basicResponseWriter) Write(p []byte) (int, error) {
	if len(w.statusCodes) == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(p)
}

type readerFromResponseWriter struct {
	basicResponseWriter
	readFromCalls int
	readFromErr   error
}

func (w *readerFromResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	w.readFromCalls++
	if len(w.statusCodes) == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := io.Copy(&w.body, r)
	if err != nil {
		return n, err
	}
	return n, w.readFromErr
}

type readerOnly struct {
	io.Reader
}

func TestResponseWritersDelegateReaderFrom(t *testing.T) {
	payload := "original audio bytes"
	delegatedErr := errors.New("delegated read error")

	for _, status := range []int{http.StatusOK, http.StatusPartialContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			base := &readerFromResponseWriter{readFromErr: delegatedErr}
			logged := &loggingWriter{
				ResponseWriter: base,
				statusCode:     http.StatusOK,
			}
			tracked := &commitTrackingWriter{ResponseWriter: logged}
			if status != http.StatusOK {
				tracked.WriteHeader(status)
			}

			n, err := io.Copy(tracked, readerOnly{Reader: strings.NewReader(payload)})
			if !errors.Is(err, delegatedErr) {
				t.Fatalf("ReadFrom error = %v, want %v", err, delegatedErr)
			}
			if n != int64(len(payload)) {
				t.Fatalf("copied bytes = %d, want %d", n, len(payload))
			}
			if got := base.body.String(); got != payload {
				t.Fatalf("response body = %q, want %q", got, payload)
			}
			if base.readFromCalls != 1 {
				t.Fatalf("underlying ReadFrom calls = %d, want 1", base.readFromCalls)
			}
			if !tracked.committed {
				t.Fatal("commitTrackingWriter did not record the response commit")
			}
			if logged.statusCode != status {
				t.Fatalf("logged status = %d, want %d", logged.statusCode, status)
			}
			if len(base.statusCodes) != 1 || base.statusCodes[0] != status {
				t.Fatalf("response commits = %v, want [%d]", base.statusCodes, status)
			}
		})
	}
}

func TestLoggingWriterReaderFromFallback(t *testing.T) {
	payload := "fallback logging bytes"
	base := &basicResponseWriter{}
	logged := &loggingWriter{
		ResponseWriter: base,
		statusCode:     http.StatusOK,
	}
	logged.WriteHeader(http.StatusPartialContent)

	n, err := io.Copy(logged, readerOnly{Reader: strings.NewReader(payload)})
	if err != nil {
		t.Fatalf("fallback copy: %v", err)
	}
	if n != int64(len(payload)) || base.body.String() != payload {
		t.Fatalf("fallback response = %d/%q, want %d/%q", n, base.body.String(), len(payload), payload)
	}
	if logged.statusCode != http.StatusPartialContent {
		t.Fatalf("logged status = %d, want %d", logged.statusCode, http.StatusPartialContent)
	}
}

func TestCommitTrackingWriterReaderFromFallback(t *testing.T) {
	payload := "fallback tracked bytes"
	base := &basicResponseWriter{}
	tracked := &commitTrackingWriter{ResponseWriter: base}

	n, err := io.Copy(tracked, readerOnly{Reader: strings.NewReader(payload)})
	if err != nil {
		t.Fatalf("fallback copy: %v", err)
	}
	if n != int64(len(payload)) || base.body.String() != payload {
		t.Fatalf("fallback response = %d/%q, want %d/%q", n, base.body.String(), len(payload), payload)
	}
	if !tracked.committed {
		t.Fatal("commitTrackingWriter did not record the response commit")
	}
	if len(base.statusCodes) != 1 || base.statusCodes[0] != http.StatusOK {
		t.Fatalf("response commits = %v, want [%d]", base.statusCodes, http.StatusOK)
	}
}
