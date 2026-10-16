package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCert writes a self-signed cert/key for cn into dir/certs and
// dir/private using the given host file name, and returns the cert DER.
func writeCert(t *testing.T, dir, host, cn string, mtime time.Time) []byte {
	t.Helper()
	c, err := selfSigned(cn, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(c.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(dir, "certs", host+".crt")
	keyFile := filepath.Join(dir, "private", host+".key")
	writePEM(t, certFile, "CERTIFICATE", c.Certificate[0], mtime)
	writePEM(t, keyFile, "PRIVATE KEY", keyDER, mtime)
	return c.Certificate[0]
}

func writePEM(t *testing.T, path, typ string, der []byte, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func getCN(t *testing.T, s *certStore) (string, int) {
	t.Helper()
	c, err := s.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf.Subject.CommonName, len(c.Certificate)
}

func TestCertStoreSelfSignedFallback(t *testing.T) {
	dir := t.TempDir()
	s, err := newCertStore(dir, "web1", discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	if cn, _ := getCN(t, s); cn != "web1" {
		t.Errorf("CN = %q", cn)
	}
	// Must not write anything to the volume.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("certStore wrote files to %s: %v", dir, entries)
	}
}

func TestCertStoreLoadsAndReloads(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Now().Add(-time.Hour)
	writeCert(t, dir, "web1", "first.example", t0)

	s, err := newCertStore(dir, "web1", discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	s.now = func() time.Time { return clock }

	if cn, _ := getCN(t, s); cn != "first.example" {
		t.Fatalf("CN = %q", cn)
	}

	writeCert(t, dir, "web1", "second.example", t0.Add(time.Minute))

	// Within the check interval the old cert is still served.
	if cn, _ := getCN(t, s); cn != "first.example" {
		t.Errorf("reloaded too early, CN = %q", cn)
	}
	clock = clock.Add(certCheckInterval + time.Second)
	if cn, _ := getCN(t, s); cn != "second.example" {
		t.Errorf("not reloaded, CN = %q", cn)
	}

	// A broken file keeps the previous certificate.
	if err := os.WriteFile(filepath.Join(dir, "certs", "web1.crt"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(certCheckInterval + time.Second)
	if cn, _ := getCN(t, s); cn != "second.example" {
		t.Errorf("broken file replaced cert, CN = %q", cn)
	}
}

func TestCertStoreInvalidAtStartup(t *testing.T) {
	dir := t.TempDir()
	writeCert(t, dir, "web1", "x", time.Now())
	if err := os.WriteFile(filepath.Join(dir, "private", "web1.key"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newCertStore(dir, "web1", discardLogger()); err == nil {
		t.Error("expected error for invalid key")
	}
}

func TestCertStoreChain(t *testing.T) {
	for _, name := range chainCandidates("web1") {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeCert(t, dir, "web1", "leaf.example", time.Now())
			ca, err := selfSigned("ca.example", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			writePEM(t, filepath.Join(dir, "certs", name), "CERTIFICATE", ca.Certificate[0], time.Now())

			s, err := newCertStore(dir, "web1", discardLogger())
			if err != nil {
				t.Fatal(err)
			}
			if _, n := getCN(t, s); n != 2 {
				t.Errorf("chain length = %d, want 2", n)
			}
		})
	}
}

func TestCertStoreChainPriorityAndDedup(t *testing.T) {
	dir := t.TempDir()
	leafDER := writeCert(t, dir, "web1", "leaf.example", time.Now())
	ca, _ := selfSigned("ca.example", time.Now())
	other, _ := selfSigned("other.example", time.Now())

	// web1.chain wins over chain.pem; it repeats the leaf (fullchain style),
	// which must not be duplicated.
	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Certificate[0]})...)
	if err := os.WriteFile(filepath.Join(dir, "certs", "web1.chain"), chain, 0o600); err != nil {
		t.Fatal(err)
	}
	writePEM(t, filepath.Join(dir, "certs", "chain.pem"), "CERTIFICATE", other.Certificate[0], time.Now())

	s, err := newCertStore(dir, "web1", discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.GetCertificate(&tls.ClientHelloInfo{})
	if len(c.Certificate) != 2 {
		t.Fatalf("chain length = %d, want 2", len(c.Certificate))
	}
	got, _ := x509.ParseCertificate(c.Certificate[1])
	if got.Subject.CommonName != "ca.example" {
		t.Errorf("chain[1] = %q, want ca.example", got.Subject.CommonName)
	}
}
