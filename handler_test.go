package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func mustConfig(t *testing.T, m map[string]string) *Config {
	t.Helper()
	if _, ok := m["HOSTNAME"]; !ok {
		m["HOSTNAME"] = "web1"
	}
	cfg, err := LoadConfig(env(m))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// acmeBackend records the path it was called with and answers with a token.
func acmeBackend(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, "token-response")
	}))
	t.Cleanup(srv.Close)
	return srv, &gotPath
}

func do(h http.Handler, method, target, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPHandler(t *testing.T) {
	h := newHTTPHandler(mustConfig(t, map[string]string{}), nil)

	tests := []struct {
		name, method, target, host string
		status                     int
		location, body             string
	}{
		{"redirect root", "GET", "/", "example.org", 301, "https://example.org/", ""},
		{"redirect keeps uri", "GET", "/foo/bar?x=1&y=%20", "example.org", 301, "https://example.org/foo/bar?x=1&y=%20", ""},
		{"redirect keeps host port", "GET", "/p", "example.org:8080", 301, "https://example.org:8080/p", ""},
		{"redirect post", "POST", "/form", "example.org", 301, "https://example.org/form", ""},
		{"redirect escaped path", "GET", "/a%2Fb", "example.org", 301, "https://example.org/a%2Fb", ""},
		{"lvs", "GET", "/_lvs.txt", "example.org", 200, "", "ok\n"},
		{"acme without backend", "GET", "/.well-known/acme-challenge/tok", "example.org", 404, "", ""},
		{"other well-known redirected", "GET", "/.well-known/security.txt", "example.org", 301, "https://example.org/.well-known/security.txt", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(h, tt.method, tt.target, tt.host)
			if rec.Code != tt.status {
				t.Errorf("status = %d, want %d", rec.Code, tt.status)
			}
			if got := rec.Header().Get("Location"); got != tt.location {
				t.Errorf("Location = %q, want %q", got, tt.location)
			}
			if tt.body != "" && rec.Body.String() != tt.body {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.body)
			}
		})
	}
}

func TestValidHost(t *testing.T) {
	good := []string{
		"example.org", "example.org:8080", "EXAMPLE.org", "a-b_c.example", "localhost",
		"192.0.2.1", "192.0.2.1:80", "[2001:db8::1]", "[2001:db8::1]:443", "[::1]",
	}
	bad := []string{
		"", ":80", "example.org:80a", "example.org:80:90", "evil.com/foo", "evil.com?x",
		"evil.com#x", "user@evil.com", `evil.com\x`, "evil .com", "[::1", "[::1]x",
		"[::1]:8a", "[evil.com]", "\x00", strings.Repeat("a", 256),
	}
	for _, h := range good {
		if !validHost(h) {
			t.Errorf("validHost(%q) = false, want true", h)
		}
	}
	for _, h := range bad {
		if validHost(h) {
			t.Errorf("validHost(%q) = true, want false", h)
		}
	}
}

func TestHTTPHandlerRejectsBadHost(t *testing.T) {
	h := newHTTPHandler(mustConfig(t, map[string]string{}), nil)
	if rec := do(h, "GET", "/x", "evil.com/@x"); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHTTPSHandler(t *testing.T) {
	cfg := mustConfig(t, map[string]string{
		"URLS": "/a%https://x.example/b? /old/(.*)%https://y.example/new/$1?",
	})
	h := newHTTPSHandler(cfg, nil)

	tests := []struct {
		name, target string
		status       int
		location     string
	}{
		{"rule with query", "/a?q=1", 301, "https://x.example/b?q=1"},
		{"rule without query", "/a", 301, "https://x.example/b"},
		{"capture", "/old/p/q?z=9", 301, "https://y.example/new/p/q?z=9"},
		{"no match", "/zzz", 404, ""},
		{"no partial match", "/a/b", 404, ""},
		{"lvs", "/_lvs.txt", 200, ""},
		{"acme without backend", "/.well-known/acme-challenge/tok", 404, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(h, "GET", tt.target, "web1")
			if rec.Code != tt.status {
				t.Errorf("status = %d, want %d", rec.Code, tt.status)
			}
			if got := rec.Header().Get("Location"); got != tt.location {
				t.Errorf("Location = %q, want %q", got, tt.location)
			}
		})
	}
}

func TestACMEProxy(t *testing.T) {
	backend, gotPath := acmeBackend(t)
	cfg := mustConfig(t, map[string]string{
		"ACME_URL": backend.URL,
		"URLS":     "/a%https://x/?",
	})
	u, _ := url.Parse(backend.URL)
	acme := newACMEProxy(u, discardLogger())

	for name, h := range map[string]http.Handler{
		"http":  newHTTPHandler(cfg, acme),
		"https": newHTTPSHandler(cfg, acme),
	} {
		t.Run(name, func(t *testing.T) {
			*gotPath = ""
			rec := do(h, "GET", "/.well-known/acme-challenge/TOKEN123", "example.org")
			if rec.Code != 200 || rec.Body.String() != "token-response" {
				t.Fatalf("got %d %q", rec.Code, rec.Body.String())
			}
			if *gotPath != "/.well-known/acme-challenge/TOKEN123" {
				t.Errorf("backend path = %q", *gotPath)
			}
		})
	}
}

func TestACMEProxyWithBasePath(t *testing.T) {
	backend, gotPath := acmeBackend(t)
	u, _ := url.Parse(backend.URL + "/prefix")
	h := newHTTPHandler(mustConfig(t, map[string]string{}), newACMEProxy(u, discardLogger()))

	rec := do(h, "GET", "/.well-known/acme-challenge/T", "example.org")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if *gotPath != "/prefix/.well-known/acme-challenge/T" {
		t.Errorf("backend path = %q", *gotPath)
	}
}

func TestACMEProxyBackendDown(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:1")
	h := newHTTPHandler(mustConfig(t, map[string]string{}), newACMEProxy(u, discardLogger()))
	if rec := do(h, "GET", "/.well-known/acme-challenge/T", "example.org"); rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestAccessLog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := accessLog(logger, "http", newHTTPHandler(mustConfig(t, map[string]string{}), nil))

	do(h, "GET", "/_lvs.txt", "example.org")
	if buf.Len() != 0 {
		t.Errorf("health check should not be logged: %s", buf.String())
	}

	do(h, "GET", "/foo?x=1", "example.org")
	line := buf.String()
	for _, want := range []string{"listener=http", "status=301", `uri="/foo?x=1"`, `location="https://example.org/foo?x=1"`} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q missing %q", line, want)
		}
	}
}
