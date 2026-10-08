// Package logtest captures the standard library logger in tests, where net/http and
// httputil write their own error lines.
package logtest

import (
	"bytes"
	"log"
	"sync"
	"testing"
)

type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// CaptureStd redirects the standard logger into a buffer until the test ends and
// returns a func that reads what was written so far. Tests using it must not run in
// parallel.
func CaptureStd(t *testing.T) func() string {
	t.Helper()
	b := &syncBuf{}
	oldOut, oldFlags, oldPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(b)
	t.Cleanup(func() {
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
		log.SetPrefix(oldPrefix)
	})
	return b.String
}
