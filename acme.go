package main

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

const acmePrefix = "/.well-known/acme-challenge/"

// newACMEProxy returns a reverse proxy that forwards ACME http-01 challenge
// requests to target, keeping the request path, e.g.
//
//	/.well-known/acme-challenge/TOKEN -> <target>/.well-known/acme-challenge/TOKEN
//
// This replaces the old Apache "ProxyPass /.well-known/acme-challenge" config.
func newACMEProxy(target *url.URL, logger *slog.Logger) http.Handler {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 10 * time.Second

	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error("acme proxy", "path", r.URL.Path, "err", err)
			http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
	}
}
