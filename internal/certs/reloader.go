package certs

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Reloader serves a certificate pair from disk and reloads it when either file's
// mtime changes (cert-manager rotations need no restart). A failed reload keeps the
// previous certificate in service.
type Reloader struct {
	name              string // log and error prefix, e.g. "tunnel tls"
	certFile, keyFile string
	log               *slog.Logger

	mu      sync.Mutex
	cert    *tls.Certificate
	certMod time.Time // mtimes of the last attempt, successful or not
	keyMod  time.Time
}

// NewReloader loads the pair once, so an unreadable or mismatched pair fails the caller
// at startup. name prefixes errors and log lines (e.g. "tunnel tls").
func NewReloader(name, certFile, keyFile string, log *slog.Logger) (*Reloader, error) {
	r := &Reloader{name: name, certFile: certFile, keyFile: keyFile, log: log}
	certMod, keyMod, err := r.mtimes()
	if err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.name, err)
	}
	r.cert, r.certMod, r.keyMod = &cert, certMod, keyMod
	return r, nil
}

func (r *Reloader) mtimes() (cert, key time.Time, err error) {
	ci, err := os.Stat(r.certFile)
	if err != nil {
		return cert, key, fmt.Errorf("%s: %w", r.name, err)
	}
	ki, err := os.Stat(r.keyFile)
	if err != nil {
		return cert, key, fmt.Errorf("%s: %w", r.name, err)
	}
	return ci.ModTime(), ki.ModTime(), nil
}

// GetCertificate implements tls.Config.GetCertificate.
func (r *Reloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	certMod, keyMod, err := r.mtimes()
	if err != nil || (certMod.Equal(r.certMod) && keyMod.Equal(r.keyMod)) {
		return r.cert, nil
	}
	// Remember the attempt so a broken pair warns once per change, not per handshake.
	r.certMod, r.keyMod = certMod, keyMod
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		r.log.Warn(r.name+" reload failed, keeping the previous certificate", "err", err)
		return r.cert, nil
	}
	r.cert = &cert
	r.log.Info(r.name + " certificate reloaded")
	return r.cert, nil
}
