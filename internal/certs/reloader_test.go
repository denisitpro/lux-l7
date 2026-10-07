package certs_test

import (
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/denisitpro/lux-l7/internal/certs"
	"github.com/denisitpro/lux-l7/internal/certs/certstest"
)

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
	r, err := certs.NewReloader("x tls", certFile, keyFile, quiet())
	if err != nil {
		t.Fatal(err)
	}
	first, _ := r.GetCertificate(nil)

	// A broken replacement keeps the previous certificate.
	if err := os.WriteFile(certFile, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetCertificate(nil)
	if err != nil || got != first {
		t.Fatalf("failed reload must keep the previous certificate, got %p want %p (err %v)", got, first, err)
	}

	// A valid pair with a newer mtime replaces it.
	certstest.SelfSigned(t, dir, "second")
	got, err = r.GetCertificate(nil)
	if err != nil || got == first {
		t.Fatalf("expected a reloaded certificate (err %v)", err)
	}
}
