package main

import (
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(env(map[string]string{"HOSTNAME": "web1"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":80" || cfg.HTTPSAddr != ":443" || cfg.SSLDir != "/etc/ssl" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if cfg.Hostname != "web1" {
		t.Errorf("Hostname = %q", cfg.Hostname)
	}
	if cfg.ACMEURL != nil {
		t.Errorf("ACMEURL = %v, want nil", cfg.ACMEURL)
	}
	if cfg.TLSEnabled() {
		t.Error("TLS should be disabled without URLS")
	}
}

func TestLoadConfigFull(t *testing.T) {
	cfg, err := LoadConfig(env(map[string]string{
		"HOSTNAME":   "web1",
		"HTTP_ADDR":  ":8080",
		"HTTPS_ADDR": ":8443",
		"SSL_DIR":    "/tmp/ssl",
		"ACME_URL":   "http://acme:8000",
		"URLS":       " /a%https://x/b?\n\t/c(.*)%https://y/$1? ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.HTTPSAddr != ":8443" || cfg.SSLDir != "/tmp/ssl" {
		t.Errorf("unexpected addrs: %+v", cfg)
	}
	if cfg.ACMEURL == nil || cfg.ACMEURL.Host != "acme:8000" {
		t.Errorf("ACMEURL = %v", cfg.ACMEURL)
	}
	if len(cfg.Rules) != 2 || !cfg.TLSEnabled() {
		t.Errorf("rules = %v", cfg.Rules)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	tests := map[string]map[string]string{
		"rule without percent": {"URLS": "/a"},
		"rule empty to":        {"URLS": "/a%"},
		"rule empty from":      {"URLS": "%https://x"},
		"bad regexp":           {"URLS": "/a(%https://x"},
		"acme relative":        {"ACME_URL": "/foo"},
		"acme bad scheme":      {"ACME_URL": "ftp://x"},
		"hostname traversal":   {"HOSTNAME": "../etc/passwd"},
		"hostname slash":       {"HOSTNAME": "a/b"},
	}
	for name, m := range tests {
		t.Run(name, func(t *testing.T) {
			if _, ok := m["HOSTNAME"]; !ok {
				m["HOSTNAME"] = "web1"
			}
			if _, err := LoadConfig(env(m)); err == nil {
				t.Error("expected error")
			}
		})
	}
}
