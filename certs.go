package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// certCheckInterval limits how often the certificate files are stat'ed.
const certCheckInterval = 10 * time.Second

// certStore serves the TLS certificate for HOSTNAME from SSL_DIR and reloads
// it when the files on disk change, so renewals need no restart.
//
// Layout (same as the old start.sh):
//
//	<dir>/certs/<host>.crt     certificate (may already contain the chain)
//	<dir>/private/<host>.key   private key
//	<dir>/certs/<chain>        optional chain, first found of chainCandidates
//
// If the certificate or key is missing, an in-memory self-signed certificate
// is used instead. Unlike start.sh nothing is ever written to disk.
type certStore struct {
	dir, host string
	logger    *slog.Logger
	now       func() time.Time

	mu        sync.Mutex
	current   *tls.Certificate
	stamp     string
	lastCheck time.Time
}

func chainCandidates(host string) []string {
	return []string{
		host + ".chain",
		host + "-chain.crt",
		host + ".chain.crt",
		"chain.crt",
		"chain.pem",
	}
}

func newCertStore(dir, host string, logger *slog.Logger) (*certStore, error) {
	s := &certStore{dir: dir, host: host, logger: logger, now: time.Now}
	if err := s.refresh(true); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *certStore) certPath() string { return filepath.Join(s.dir, "certs", s.host+".crt") }
func (s *certStore) keyPath() string  { return filepath.Join(s.dir, "private", s.host+".key") }

func (s *certStore) chainPath() string {
	for _, name := range chainCandidates(s.host) {
		p := filepath.Join(s.dir, "certs", name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// GetCertificate implements tls.Config.GetCertificate.
func (s *certStore) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	if err := s.refresh(false); err != nil {
		s.logger.Error("certificate reload failed, keeping previous", "err", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current, nil
}

// refresh reloads the certificate if the files changed. With force it
// ignores the check interval and returns errors even if a certificate is
// already loaded.
func (s *certStore) refresh(force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if !force && now.Sub(s.lastCheck) < certCheckInterval {
		return nil
	}
	s.lastCheck = now

	certFile, keyFile, chainFile := s.certPath(), s.keyPath(), s.chainPath()
	stamp, err := fileStamp(certFile, keyFile, chainFile)
	if errors.Is(err, fs.ErrNotExist) {
		if s.stamp == "self-signed" {
			return nil
		}
		cert, err := selfSigned(s.host, now)
		if err != nil {
			return err
		}
		s.current, s.stamp = cert, "self-signed"
		s.logger.Warn("certificate or key not found, using self-signed certificate",
			"cert", certFile, "key", keyFile, "host", s.host)
		return nil
	}
	if err != nil {
		return err
	}
	if stamp == s.stamp {
		return nil
	}

	cert, err := loadKeyPair(certFile, keyFile, chainFile)
	if err != nil {
		return err
	}
	s.current, s.stamp = cert, stamp
	s.logger.Info("certificate loaded",
		"cert", certFile, "chain", chainFile,
		"subject", cert.Leaf.Subject.String(),
		"not_after", cert.Leaf.NotAfter.Format(time.RFC3339),
		"chain_len", len(cert.Certificate))
	return nil
}

// fileStamp returns a string that changes whenever any of the files change.
// Empty paths are skipped. Missing cert/key yields fs.ErrNotExist.
func fileStamp(paths ...string) (string, error) {
	var b bytes.Buffer
	for _, p := range paths {
		if p == "" {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s:%d:%d;", p, fi.Size(), fi.ModTime().UnixNano())
	}
	return b.String(), nil
}

func loadKeyPair(certFile, keyFile, chainFile string) (*tls.Certificate, error) {
	certPEM, err := os.ReadFile(certFile) // #nosec G304 -- path built from trusted config
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyFile) // #nosec G304 -- path built from trusted config
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%s / %s: %w", certFile, keyFile, err)
	}
	if chainFile != "" {
		chainPEM, err := os.ReadFile(chainFile) // #nosec G304 -- path built from trusted config
		if err != nil {
			return nil, err
		}
		if err := appendChain(&cert, chainPEM); err != nil {
			return nil, fmt.Errorf("%s: %w", chainFile, err)
		}
	}
	return &cert, nil
}

// appendChain adds the CERTIFICATE blocks of chainPEM to cert, skipping
// certificates that are already present (e.g. cert file is a fullchain).
func appendChain(cert *tls.Certificate, chainPEM []byte) error {
	found := false
	for {
		var block *pem.Block
		block, chainPEM = pem.Decode(chainPEM)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		found = true
		dup := false
		for _, have := range cert.Certificate {
			if bytes.Equal(have, block.Bytes) {
				dup = true
				break
			}
		}
		if !dup {
			cert.Certificate = append(cert.Certificate, block.Bytes)
		}
	}
	if !found {
		return errors.New("no certificates found in chain file")
	}
	return nil
}

// selfSigned creates an ephemeral ECDSA certificate for host, the in-memory
// replacement for Debian's ssl-cert "snakeoil" certificate.
func selfSigned(host string, now time.Time) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		DNSNames:              []string{host},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}
