// Package discardlog provides the logger that net/http and httputil use for their
// own error lines. Those lines carry client and upstream addresses, which the JSON
// log must never show, so every server and reverse proxy sets it as ErrorLog.
package discardlog

import (
	"io"
	"log"
)

// New returns a standard logger that drops everything.
func New() *log.Logger { return log.New(io.Discard, "", 0) }
