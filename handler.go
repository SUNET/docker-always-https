package main

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const lvsPath = "/_lvs.txt"

// common serves the paths shared by both listeners: the load balancer
// health check and ACME challenges. It reports whether it handled r.
func common(acme http.Handler, w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == lvsPath:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
		return true
	case strings.HasPrefix(r.URL.Path, acmePrefix):
		if acme == nil {
			http.NotFound(w, r)
		} else {
			acme.ServeHTTP(w, r)
		}
		return true
	}
	return false
}

// newHTTPHandler redirects every request to the same host and URI on https,
// except the health check and ACME challenges.
func newHTTPHandler(cfg *Config, acme http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if common(acme, w, r) {
			return
		}
		host := r.Host
		if host == "" {
			host = cfg.Hostname
		}
		if !validHost(host) {
			http.Error(w, "invalid Host header", http.StatusBadRequest)
			return
		}
		// Redirecting to the client supplied Host is the whole point of this
		// service (same as Apache's %{HTTP_HOST}); validHost ensures it can
		// only change the scheme, not inject a different URL structure.
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently) // #nosec G710
	})
}

// validHost reports whether h is a plain "hostname", "hostname:port",
// "IPv4:port" or "[IPv6]:port" with no characters that could alter the
// structure of the resulting URL (/, @, ?, #, \, whitespace, ...).
func validHost(h string) bool {
	if len(h) > 255 {
		return false
	}
	name, port, allowed := h, "", hostChars
	if strings.HasPrefix(h, "[") {
		end := strings.IndexByte(h, ']')
		if end < 0 {
			return false
		}
		name, port, allowed = h[1:end], h[end+1:], ipv6Chars
	} else if i := strings.LastIndexByte(h, ':'); i >= 0 {
		name, port = h[:i], h[i:]
	}
	if name == "" || !onlyChars(name, allowed) {
		return false
	}
	return port == "" || (len(port) > 1 && port[0] == ':' && onlyChars(port[1:], "0123456789"))
}

const (
	hostChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-_"
	ipv6Chars = "0123456789abcdefABCDEF:."
)

func onlyChars(s, allowed string) bool {
	for _, c := range s {
		if !strings.ContainsRune(allowed, c) {
			return false
		}
	}
	return true
}

// newHTTPSHandler applies the URLS rewrite rules; anything that does not
// match is a 404.
func newHTTPSHandler(cfg *Config, acme http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if common(acme, w, r) {
			return
		}
		if loc, ok := MatchRules(cfg.Rules, r.URL.Path, r.URL.RawQuery); ok {
			// Set Location directly: http.Redirect would clean/rewrite
			// relative targets, the rule author's string must be kept as is.
			w.Header().Set("Location", loc)
			w.WriteHeader(http.StatusMovedPermanently)
			return
		}
		http.NotFound(w, r)
	})
}

// statusRecorder captures the response status for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer
// (needed by httputil.ReverseProxy for flushing).
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// accessLog logs one line per request. Health checks are not logged to keep
// the output readable behind a load balancer that polls every second.
func accessLog(logger *slog.Logger, listener string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if r.URL.Path == lvsPath {
			return
		}
		attrs := []any{
			"listener", listener,
			"remote", r.RemoteAddr,
			"method", r.Method,
			"host", r.Host,
			"uri", r.URL.RequestURI(),
			"status", rec.status,
			"duration", time.Since(start),
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			attrs = append(attrs, "location", loc)
		}
		logger.Info("request", attrs...)
	})
}
