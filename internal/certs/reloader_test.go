package certs_test

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/denisitpro/lux-l7/internal/certs"
	"github.com/denisitpro/lux-l7/internal/certs/certstest"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestNewReloaderRejectsBadPair(t *testing.T) {
	certFile, keyFile := certstest.SelfSigned(t, t.TempDir(), "a")
	_, otherKey := certstest.SelfSigned(t, t.TempDir(), "b")

	if _, err := certs.NewReloader("x tls", certFile, otherKey, quiet()); err == nil {
		t.Fatal("mismatched pair must fail")
	}
	if _, err := certs.NewReloader("x tls", certFile+".missing", keyFile, quiet()); err == nil {
		t.Fatal("missing cert must fail")
	}
	if _, err := certs.NewReloader("x tls", certFile, keyFile, quiet()); err != nil {
		t.Fatal(err)
	}
}

func TestReloaderReloadsOnMtimeAndKeepsPreviousOnFailure(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := certstest.SelfSigned(t, dir, "first")
	past := time.Now().Add(-time.Hour)
	for _, f := range []string{certFile, keyFile} {
		if err := os.Chtimes(f, past, past); err != nil {
			t.Fatal(err)
		}
	}
	clock := newClock()
	r, err := certs.NewReloader("x tls", certFile, keyFile, quiet(), certs.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := r.GetCertificate(nil)

	// A broken replacement keeps the previous certificate.
	if err := os.WriteFile(certFile, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	got, err := r.GetCertificate(nil)
	if err != nil || got != first {
		t.Fatalf("failed reload must keep the previous certificate, got %p want %p (err %v)", got, first, err)
	}

	// A valid pair with a newer mtime replaces it.
	certstest.SelfSigned(t, dir, "second")
	clock.Advance(time.Second)
	got, err = r.GetCertificate(nil)
	if err != nil || got == first {
		t.Fatalf("expected a reloaded certificate (err %v)", err)
	}
}

// Inside the check interval a changed file is not noticed (no stat on the fast path);
// the first handshake after the interval picks it up.
func TestReloaderChecksAtMostOncePerInterval(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := certstest.SelfSigned(t, dir, "first")
	past := time.Now().Add(-time.Hour)
	for _, f := range []string{certFile, keyFile} {
		if err := os.Chtimes(f, past, past); err != nil {
			t.Fatal(err)
		}
	}
	clock := newClock()
	r, err := certs.NewReloader("x tls", certFile, keyFile, quiet(), certs.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := r.GetCertificate(nil)

	certstest.SelfSigned(t, dir, "second")
	clock.Advance(500 * time.Millisecond)
	if got, _ := r.GetCertificate(nil); got != first {
		t.Fatal("certificate must not be re-read inside the check interval")
	}
	clock.Advance(500 * time.Millisecond)
	if got, _ := r.GetCertificate(nil); got == first {
		t.Fatal("certificate must be reloaded at the first handshake after the interval")
	}
}

// A broken replacement warns once per file change, not once per check or handshake.
func TestReloaderWarnsOncePerChange(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := certstest.SelfSigned(t, dir, "first")
	past := time.Now().Add(-time.Hour)
	for _, f := range []string{certFile, keyFile} {
		if err := os.Chtimes(f, past, past); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	clock := newClock()
	r, err := certs.NewReloader("x tls", certFile, keyFile, log, certs.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		clock.Advance(2 * time.Second)
		if _, err := r.GetCertificate(nil); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(buf.String(), "reload failed"); n != 1 {
		t.Fatalf("reload failure warned %d times, want 1\n%s", n, buf.String())
	}
}

func TestStartupLogsCertificateAttributes(t *testing.T) {
	certFile, keyFile := certstest.SelfSigned(t, t.TempDir(), "log.example.com")
	var buf bytes.Buffer
	if _, err := certs.NewReloader("x tls", certFile, keyFile, slog.New(slog.NewTextHandler(&buf, nil))); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"x tls certificate loaded", "key_type=ecdsa", "curve=P-256", "subject_cn=log.example.com", "not_after="} {
		if !strings.Contains(out, want) {
			t.Fatalf("log lacks %q:\n%s", want, out)
		}
	}
}
