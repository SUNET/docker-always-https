// Command always-https redirects all plain HTTP traffic to https on the same
// host. Optionally it also terminates TLS and serves a fixed set of
// redirects (URLS), and proxies ACME http-01 challenges (ACME_URL).
//
// It replaces the Apache based image that previously lived in this repo and
// keeps its environment variables, ports and certificate layout.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the local HTTP listener and exit 0 if healthy")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}

	if *healthcheck {
		if err := probe(cfg.HTTPAddr); err != nil {
			fmt.Fprintln(os.Stderr, "unhealthy:", err)
			os.Exit(1)
		}
		return
	}

	// Access log to stdout, everything else to stderr (as with Apache).
	errLog := slog.New(slog.NewTextHandler(os.Stderr, nil))
	accLog := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, errLog, accLog); err != nil {
		errLog.Error("exiting", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg *Config, errLog, accLog *slog.Logger) error {
	var acme http.Handler
	if cfg.ACMEURL != nil {
		acme = newACMEProxy(cfg.ACMEURL, errLog)
	}

	servers := []*http.Server{
		newServer(cfg.HTTPAddr, accessLog(accLog, "http", newHTTPHandler(cfg, acme)), errLog),
	}

	if cfg.TLSEnabled() {
		store, err := newCertStore(cfg.SSLDir, cfg.Hostname, errLog)
		if err != nil {
			return fmt.Errorf("certificate: %w", err)
		}
		srv := newServer(cfg.HTTPSAddr, accessLog(accLog, "https", newHTTPSHandler(cfg, acme)), errLog)
		srv.TLSConfig = &tls.Config{
			MinVersion:     tls.VersionTLS12,
			GetCertificate: store.GetCertificate,
		}
		servers = append(servers, srv)
		for _, r := range cfg.Rules {
			errLog.Info("rewrite rule", "rule", r.String())
		}
	}

	errc := make(chan error, len(servers))
	for _, srv := range servers {
		ln, err := net.Listen("tcp", srv.Addr)
		if err != nil {
			return err
		}
		errLog.Info("listening", "addr", ln.Addr().String(), "tls", srv.TLSConfig != nil, "version", version)
		go func() {
			if srv.TLSConfig != nil {
				errc <- srv.ServeTLS(ln, "", "")
			} else {
				errc <- srv.Serve(ln)
			}
		}()
	}

	select {
	case <-ctx.Done():
		errLog.Info("shutting down")
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var errs []error
	for _, srv := range servers {
		errs = append(errs, srv.Shutdown(shutdownCtx))
	}
	return errors.Join(errs...)
}

func newServer(addr string, h http.Handler, errLog *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(errLog.Handler(), slog.LevelWarn),
	}
}

// probe performs the container health check against the local HTTP
// listener. Used because a scratch image has no curl/wget.
func probe(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + lvsPath)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", resp.Status)
	}
	return nil
}
