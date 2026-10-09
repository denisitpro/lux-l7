package bind

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
)

func TestListenBinds(t *testing.T) {
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if ln.Addr().String() == "" {
		t.Fatal("empty addr")
	}
}

func TestListenTakenPortKeepsPlainError(t *testing.T) {
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	_, err = Listen(ln.Addr().String())
	if err == nil {
		t.Fatal("expected bind error")
	}
	if strings.Contains(err.Error(), "privileged") {
		t.Fatalf("address-in-use must not blame privileges: %v", err)
	}
}

func TestHintWrapsEACCES(t *testing.T) {
	err := hint(fmt.Errorf("listen tcp :449: bind: %w", syscall.EACCES))
	if !errors.Is(err, syscall.EACCES) || !strings.Contains(err.Error(), "-host image") {
		t.Fatalf("got %v", err)
	}
}
