// Package listenlog emits the one startup line every listener logs after its
// socket is bound, so all listeners share one message and one field set.
package listenlog

import "log/slog"

// Up logs `listener up` for a bound listener: which listener it is (main,
// tunnel, metrics), where it listens, what it serves and whether it speaks
// TLS. extra carries listener-specific attributes (routes, h2c, ...).
func Up(log *slog.Logger, listener, addr, mode string, tls bool, extra ...any) {
	attrs := append([]any{"listener", listener, "addr", addr, "mode", mode, "tls", tls}, extra...)
	log.Info("listener up", attrs...)
}
