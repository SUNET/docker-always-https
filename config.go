package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Config holds the runtime configuration, read from the same environment
// variables the old Apache based start.sh used.
type Config struct {
	// HTTPAddr is the listen address of the plain HTTP redirector.
	HTTPAddr string
	// HTTPSAddr is the listen address of the TLS listener (only used when
	// rewrite rules are configured).
	HTTPSAddr string
	// SSLDir is the root of the certificate tree (certs/ and private/).
	SSLDir string
	// Hostname selects which certificate files to load.
	Hostname string
	// ACMEURL, when set, receives proxied /.well-known/acme-challenge/ requests.
	ACMEURL *url.URL
	// Rules are the parsed URLS rewrite rules. A non-empty list enables TLS.
	Rules []Rule
}

// TLSEnabled reports whether the HTTPS listener should be started.
func (c *Config) TLSEnabled() bool { return len(c.Rules) > 0 }

// LoadConfig builds a Config from environment lookups.
//
// Supported variables:
//
//	URLS        whitespace separated list of "from%to" rewrite rules
//	ACME_URL    base URL that ACME http-01 challenges are proxied to
//	HOSTNAME    name used to find the certificate (set by Docker)
//	HTTP_ADDR   listen address for HTTP  (default ":80")
//	HTTPS_ADDR  listen address for HTTPS (default ":443")
//	SSL_DIR     certificate directory    (default "/etc/ssl")
func LoadConfig(getenv func(string) string) (*Config, error) {
	cfg := &Config{
		HTTPAddr:  envOr(getenv, "HTTP_ADDR", ":80"),
		HTTPSAddr: envOr(getenv, "HTTPS_ADDR", ":443"),
		SSLDir:    envOr(getenv, "SSL_DIR", "/etc/ssl"),
		Hostname:  getenv("HOSTNAME"),
	}

	if cfg.Hostname == "" {
		h, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("HOSTNAME not set and os.Hostname failed: %w", err)
		}
		cfg.Hostname = h
	}
	if strings.ContainsAny(cfg.Hostname, `/\`) || strings.Contains(cfg.Hostname, "..") {
		return nil, fmt.Errorf("HOSTNAME %q is not a valid file name", cfg.Hostname)
	}

	if raw := strings.TrimSpace(getenv("ACME_URL")); raw != "" {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("ACME_URL: %w", err)
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("ACME_URL %q: must be an absolute http(s) URL", raw)
		}
		cfg.ACMEURL = u
	}

	rules, err := ParseRules(getenv("URLS"))
	if err != nil {
		return nil, fmt.Errorf("URLS: %w", err)
	}
	cfg.Rules = rules

	return cfg, nil
}

// ParseRules parses the URLS value: whitespace separated "from%to" pairs.
func ParseRules(s string) ([]Rule, error) {
	var (
		rules []Rule
		errs  []error
	)
	for entry := range strings.FieldsSeq(s) {
		from, to, ok := strings.Cut(entry, "%")
		if !ok || from == "" || to == "" {
			errs = append(errs, fmt.Errorf("%q: expected from%%to", entry))
			continue
		}
		r, err := NewRule(from, to)
		if err != nil {
			errs = append(errs, fmt.Errorf("%q: %w", entry, err))
			continue
		}
		rules = append(rules, r)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return rules, nil
}

func envOr(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}
