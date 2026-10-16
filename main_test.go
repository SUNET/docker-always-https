package main

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"testing"
	"time"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

// TestRunEndToEnd starts both listeners and exercises them over the network.
func TestRunEndToEnd(t *testing.T) {
	httpAddr, httpsAddr := freeAddr(t), freeAddr(t)
	cfg := mustConfig(t, map[string]string{
		"HTTP_ADDR":  httpAddr,
		"HTTPS_ADDR": httpsAddr,
		"SSL_DIR":    t.TempDir(),
		"URLS":       "/a%https://x.example/b?",
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, discardLogger(), discardLogger()) }()

	waitFor(t, httpAddr)
	waitFor(t, httpsAddr)

	if err := probe(httpAddr); err != nil {
		t.Errorf("probe: %v", err)
	}

	client := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- self-signed test cert
		},
	}

	resp, err := client.Get("http://" + httpAddr + "/foo?x=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 301 || resp.Header.Get("Location") != "https://"+httpAddr+"/foo?x=1" {
		t.Errorf("http: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp, err = client.Get("https://" + httpsAddr + "/a?q=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 301 || resp.Header.Get("Location") != "https://x.example/b?q=1" {
		t.Errorf("https: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp.TLS == nil || resp.TLS.Version < tls.VersionTLS12 {
		t.Errorf("unexpected TLS state: %+v", resp.TLS)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not shut down")
	}
}

func TestRunHTTPOnly(t *testing.T) {
	httpAddr, httpsAddr := freeAddr(t), freeAddr(t)
	cfg := mustConfig(t, map[string]string{"HTTP_ADDR": httpAddr, "HTTPS_ADDR": httpsAddr})

	ctx := t.Context()
	go func() { _ = run(ctx, cfg, discardLogger(), discardLogger()) }()
	waitFor(t, httpAddr)

	if c, err := net.DialTimeout("tcp", httpsAddr, 200*time.Millisecond); err == nil {
		_ = c.Close()
		t.Error("HTTPS listener should not be started without URLS")
	}
}

func TestProbeFails(t *testing.T) {
	if err := probe(freeAddr(t)); err == nil {
		t.Error("expected probe to fail with nothing listening")
	}
}

func waitFor(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s not listening", addr)
}
