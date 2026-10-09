package certs

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// checkInterval is the minimum time between two mtime checks. Handshakes inside
// the interval are served from memory without touching the disk.
const checkInterval = time.Second

// Reloader serves a certificate pair from disk and reloads it when either file's
// mtime changes (cert-manager rotations need no restart). The mtimes are checked at
// most once per checkInterval, on the handshake that finds the interval elapsed. A
// failed reload keeps the previous certificate in service.
type Reloader struct {
	name              string // log and error prefix, e.g. "tunnel tls"
	certFile, keyFile string
	log               *slog.Logger

	now func() time.Time

	cert      atomic.Pointer[tls.Certificate] // lock-free fast path
	lastCheck atomic.Int64                    // unix nanos of the last mtime check

	mu      sync.Mutex // serialises reloads; guards the mtimes below
	certMod time.Time  // mtimes of the last attempt, successful or not
	keyMod  time.Time
}

// Option customises a Reloader.
type Option func(*Reloader)

// WithClock replaces the time source used for the check interval (tests).
func WithClock(now func() time.Time) Option {
	return func(r *Reloader) { r.now = now }
}

// NewReloader loads the pair once, so an unreadable or mismatched pair fails the caller
// at startup. name prefixes errors and log lines (e.g. "tunnel tls").
func NewReloader(name, certFile, keyFile string, log *slog.Logger, opts ...Option) (*Reloader, error) {
	r := &Reloader{name: name, certFile: certFile, keyFile: keyFile, log: log, now: time.Now}
	for _, o := range opts {
		o(r)
	}
	certMod, keyMod, err := r.mtimes()
	if err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.name, err)
	}
	r.cert.Store(&cert)
	r.certMod, r.keyMod = certMod, keyMod
	r.lastCheck.Store(r.now().UnixNano())
	r.logCert(" certificate loaded", &cert)
	return r, nil
}

// logCert logs the public attributes of cert; key material is never read.
func (r *Reloader) logCert(msg string, cert *tls.Certificate) {
	if info, ok := describePair(cert); ok {
		r.log.Info(r.name+msg, info.Attrs()...)
		return
	}
	r.log.Info(r.name + msg)
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

// GetCertificate implements tls.Config.GetCertificate. Inside the check interval it
// returns the cached certificate without a lock or a syscall; the one handshake that
// finds the interval elapsed checks the mtimes and reloads if they changed.
func (r *Reloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	now := r.now().UnixNano()
	last := r.lastCheck.Load()
	if now-last < int64(checkInterval) || !r.lastCheck.CompareAndSwap(last, now) {
		return r.cert.Load(), nil
	}
	r.reload()
	return r.cert.Load(), nil
}

func (r *Reloader) reload() {
	r.mu.Lock()
	defer r.mu.Unlock()
	certMod, keyMod, err := r.mtimes()
	if err != nil || (certMod.Equal(r.certMod) && keyMod.Equal(r.keyMod)) {
		return
	}
	// Remember the attempt so a broken pair warns once per change, not per check.
	r.certMod, r.keyMod = certMod, keyMod
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		r.log.Warn(r.name+" reload failed, keeping the previous certificate", "err", err)
		return
	}
	r.cert.Store(&cert)
	r.logCert(" certificate reloaded", &cert)
}
