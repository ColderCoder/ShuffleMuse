package api

import (
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/config"
)

func SecurityMiddleware(cfg *config.Config, next http.Handler) http.Handler {
	allowedHosts := make([]string, 0)
	if cfg != nil {
		allowedHosts = cfg.AllowedHosts
	}
	if len(allowedHosts) == 0 {
		allowedHosts = []string{"localhost", "127.0.0.1", "::1"}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w.Header())
		if !hostAllowed(r.Host, allowedHosts) {
			writeError(w, http.StatusBadRequest, "INVALID_HOST", "request host is not allowed")
			return
		}
		if r.Method == http.MethodPost || r.Method == http.MethodDelete {
			if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
				writeError(w, http.StatusForbidden, "CSRF_BLOCKED", "cross-site request blocked")
				return
			}
			if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
				parsed, err := url.Parse(origin)
				if err != nil || parsed.Scheme == "" || parsed.Host == "" || !strings.EqualFold(parsed.Host, r.Host) {
					writeError(w, http.StatusForbidden, "CSRF_BLOCKED", "request origin does not match host")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func setSecurityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; connect-src 'self'; form-action 'self'; frame-ancestors 'self'; frame-src 'self' blob:; img-src 'self' data: blob:; media-src 'self' blob:; object-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'")
	header.Set("X-Frame-Options", "SAMEORIGIN")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "same-origin")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
}

func hostAllowed(authority string, allowed []string) bool {
	raw := strings.ToLower(strings.TrimSpace(authority))
	host := raw
	if parsed, _, err := net.SplitHostPort(raw); err == nil {
		host = strings.Trim(parsed, "[]")
	} else {
		host = strings.Trim(raw, "[]")
	}
	host = strings.TrimSuffix(host, ".")
	for _, candidate := range allowed {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		if candidate == raw || strings.TrimSuffix(strings.Trim(candidate, "[]"), ".") == host {
			return true
		}
	}
	return false
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &loggingWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(lw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, lw.statusCode, time.Since(start))
	})
}

type loggingWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lw *loggingWriter) Unwrap() http.ResponseWriter { return lw.ResponseWriter }

func (lw *loggingWriter) Flush() {
	if flusher, ok := lw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (lw *loggingWriter) WriteHeader(code int) {
	lw.statusCode = code
	lw.ResponseWriter.WriteHeader(code)
}
