package reqlog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/denisitpro/lux-l7/internal/metrics"
	"github.com/denisitpro/lux-l7/internal/reqlog"
)

// syncBuffer is a log destination safe to read while a logger goroutine writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newLogger returns a request logger writing JSON at level, and the captured output.
func newLogger(level slog.Level) (*reqlog.Logger, *syncBuffer) {
	out := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level}))
	return reqlog.New(log), out
}

// lines parses the captured JSON log lines.
func lines(t *testing.T, out *syncBuffer) []map[string]any {
	t.Helper()
	var res []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if raw == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("not JSON: %q", raw)
		}
		res = append(res, m)
	}
	return res
}

// wantNoLines fails when anything was logged.
func wantNoLines(t *testing.T, out *syncBuffer) {
	t.Helper()
	if s := out.String(); s != "" {
		t.Fatalf("expected no log output, got:\n%s", s)
	}
}

func routeReq(idx int, status int, d time.Duration) metrics.Request {
	return metrics.Request{
		Mode: metrics.ModeRoute, Method: "GET", Route: idx, RouteHost: "example.com",
		UpstreamHost: "origin" + string(rune('0'+idx)) + ".example.com", Status: status, Duration: d,
	}
}

func upstreamFail(idx int, reason metrics.UpstreamError) metrics.Request {
	r := routeReq(idx, 502, time.Millisecond)
	r.UpstreamError = reason
	return r
}

func directReq(mode metrics.Mode, reason metrics.UpstreamError) metrics.Request {
	return metrics.Request{
		Mode: mode, Method: "POST", IngressHost: "front.example.com", Authority: "api.example.com:443",
		Route: -1, Status: 502, Duration: 12 * time.Millisecond, Bytes: 7, UpstreamError: reason,
	}
}

func TestRouteSummaryAtInfo(t *testing.T) {
	l, out := newLogger(slog.LevelInfo)
	for i := 1; i <= 18; i++ {
		l.Finished(routeReq(0, 200, time.Duration(i)*time.Millisecond))
	}
	l.Finished(upstreamFail(0, metrics.UpstreamOther))
	slow := upstreamFail(0, metrics.UpstreamTimeout)
	slow.Duration = 20 * time.Millisecond
	l.Finished(slow)
	l.Finished(routeReq(1, 404, 3*time.Millisecond))
	wantNoLines(t, out) // nothing per request, nothing before the interval ends

	l.Flush()
	got := lines(t, out)
	if len(got) != 2 {
		t.Fatalf("want 2 summaries, got %d:\n%s", len(got), out.String())
	}
	s := got[0]
	if s["msg"] != "route summary" || s["level"] != "INFO" || s["route"] != float64(0) ||
		s["upstream"] != "origin0.example.com" || s["requests"] != float64(20) {
		t.Fatalf("route 0 summary: %v", s)
	}
	if st := s["status"].(map[string]any); st["2xx"] != float64(18) || st["5xx"] != float64(2) || len(st) != 2 {
		t.Fatalf("status classes: %v", st)
	}
	if er := s["errors"].(map[string]any); er["other"] != float64(1) || er["timeout"] != float64(1) || len(er) != 2 {
		t.Fatalf("errors by reason: %v", er)
	}
	// 20 samples: 1..18 ms, 1 ms and 20 ms. Nearest rank: p50 is the 10th, p95 the 19th smallest.
	if s["p50_ms"] != float64(9) || s["p95_ms"] != float64(18) {
		t.Fatalf("latency p50=%v p95=%v", s["p50_ms"], s["p95_ms"])
	}
	if got[1]["route"] != float64(1) || got[1]["requests"] != float64(1) {
		t.Fatalf("route 1 summary: %v", got[1])
	}
	if _, has := got[1]["errors"]; has {
		t.Fatalf("a route without errors must not carry an errors group: %v", got[1])
	}
}

func TestRouteSilentWhenIdleAndIntervalResets(t *testing.T) {
	l, out := newLogger(slog.LevelInfo)
	l.Flush()
	wantNoLines(t, out)

	l.Finished(routeReq(0, 200, time.Millisecond))
	l.Flush()
	if n := len(lines(t, out)); n != 1 {
		t.Fatalf("want 1 summary, got %d", n)
	}
	l.Flush() // an idle interval after traffic: silent again
	if n := len(lines(t, out)); n != 1 {
		t.Fatalf("idle interval logged:\n%s", out.String())
	}
	l.Finished(routeReq(0, 200, 4*time.Millisecond))
	l.Flush()
	got := lines(t, out)
	if len(got) != 2 || got[1]["requests"] != float64(1) || got[1]["p50_ms"] != float64(4) {
		t.Fatalf("interval did not reset:\n%s", out.String())
	}
}

func TestRouteNoSummaryAtWarn(t *testing.T) {
	l, out := newLogger(slog.LevelWarn)
	l.Finished(routeReq(0, 200, time.Millisecond))
	l.Flush()
	wantNoLines(t, out)
}

func TestRouteFailingAndRecovered(t *testing.T) {
	l, out := newLogger(slog.LevelWarn)
	for i := 0; i < reqlog.FailingAfter-1; i++ {
		l.Finished(upstreamFail(0, metrics.UpstreamOther))
	}
	wantNoLines(t, out) // not failing yet

	// Another route's errors do not count toward route 0.
	l.Finished(upstreamFail(1, metrics.UpstreamOther))
	wantNoLines(t, out)

	l.Finished(upstreamFail(0, metrics.UpstreamTimeout))
	got := lines(t, out)
	if len(got) != 1 || got[0]["msg"] != "upstream failing" || got[0]["level"] != "WARN" ||
		got[0]["route"] != float64(0) || got[0]["upstream"] != "origin0.example.com" ||
		got[0]["consecutive_errors"] != float64(reqlog.FailingAfter) || got[0]["reason"] != "timeout" {
		t.Fatalf("failing line: %s", out.String())
	}

	// Repeats are collapsed; a refusal and a client cancel are neutral.
	l.Finished(upstreamFail(0, metrics.UpstreamOther))
	l.Finished(upstreamFail(0, metrics.UpstreamOther))
	loop := routeReq(0, 508, time.Millisecond)
	loop.Rejection = metrics.RejectLoopDetected
	l.Finished(loop)
	l.Finished(upstreamFail(0, metrics.UpstreamCanceled))
	if n := len(lines(t, out)); n != 1 {
		t.Fatalf("repeats must be collapsed, got %d lines:\n%s", n, out.String())
	}

	l.Finished(routeReq(0, 200, time.Millisecond))
	got = lines(t, out)
	if len(got) != 2 || got[1]["msg"] != "upstream recovered" || got[1]["route"] != float64(0) ||
		got[1]["consecutive_errors"] != float64(reqlog.FailingAfter+2) {
		t.Fatalf("recovered line: %s", out.String())
	}

	// Healthy again: further successes are silent, and a short error run resets quietly.
	l.Finished(routeReq(0, 200, time.Millisecond))
	for i := 0; i < reqlog.FailingAfter-1; i++ {
		l.Finished(upstreamFail(0, metrics.UpstreamOther))
	}
	l.Finished(routeReq(0, 200, time.Millisecond))
	if n := len(lines(t, out)); n != 2 {
		t.Fatalf("unexpected lines:\n%s", out.String())
	}

	// It can fail again.
	for i := 0; i < reqlog.FailingAfter; i++ {
		l.Finished(upstreamFail(0, metrics.UpstreamOther))
	}
	if got = lines(t, out); len(got) != 3 || got[2]["msg"] != "upstream failing" {
		t.Fatalf("second failing line missing:\n%s", out.String())
	}
}

func TestRouteUpstream5xxIsAResponseNotAnError(t *testing.T) {
	l, out := newLogger(slog.LevelWarn)
	for i := 0; i < reqlog.FailingAfter+1; i++ {
		l.Finished(routeReq(0, 503, time.Millisecond)) // the upstream answered
	}
	wantNoLines(t, out)
}

func TestRouteUnmatchedRequestIsIgnored(t *testing.T) {
	l, out := newLogger(slog.LevelDebug)
	r := routeReq(0, 404, time.Millisecond)
	r.Route, r.Rejection = -1, metrics.RejectNoRoute
	l.Finished(r)
	l.Flush()
	wantNoLines(t, out)
}

func TestRouteDebugLine(t *testing.T) {
	l, out := newLogger(slog.LevelDebug)
	r := routeReq(2, 200, 1500*time.Microsecond)
	r.Bytes = 42
	r.IngressHost = "client-supplied.example.com"
	l.Finished(r)
	got := lines(t, out)
	if len(got) != 1 {
		t.Fatalf("want one line per request:\n%s", out.String())
	}
	want := map[string]any{
		"level": "DEBUG", "msg": "request", "mode": "route", "route": float64(2),
		"upstream": "origin2.example.com", "status": float64(200), "duration_ms": 1.5, "bytes": float64(42),
	}
	for k, v := range want {
		if got[0][k] != v {
			t.Errorf("%s = %v, want %v (line: %v)", k, got[0][k], v, got[0])
		}
	}
	if strings.Contains(out.String(), "client-supplied") {
		t.Fatalf("client host leaked: %s", out.String())
	}
}

func TestHeaderAndTunnelSilentAtInfo(t *testing.T) {
	l, out := newLogger(slog.LevelInfo)
	for _, m := range []metrics.Mode{metrics.ModeHeader, metrics.ModeTunnel} {
		ok := directReq(m, "")
		ok.Status = 200
		l.Finished(ok)
		l.Finished(directReq(m, ""))
		rej := directReq(m, "")
		rej.Rejection, rej.Status = metrics.RejectForbiddenHost, 403
		l.Finished(rej)
	}
	l.Flush()
	wantNoLines(t, out)
}

func TestHeaderAndTunnelDebugLine(t *testing.T) {
	l, out := newLogger(slog.LevelDebug)
	l.Finished(directReq(metrics.ModeHeader, ""))
	tun := directReq(metrics.ModeTunnel, "")
	tun.Method, tun.Status, tun.Bytes = "CONNECT", 200, 1024
	l.Finished(tun)
	rej := directReq(metrics.ModeHeader, "")
	rej.Authority, rej.Rejection, rej.Status = "", metrics.RejectBadTarget, 400
	l.Finished(rej)

	got := lines(t, out)
	if len(got) != 3 {
		t.Fatalf("want one line per request:\n%s", out.String())
	}
	h := map[string]any{
		"level": "DEBUG", "msg": "request", "mode": "header", "method": "POST", "target": "api.example.com:443",
		"status": float64(502), "duration_ms": float64(12), "bytes": float64(7),
	}
	for k, v := range h {
		if got[0][k] != v {
			t.Errorf("header %s = %v, want %v", k, got[0][k], v)
		}
	}
	if got[1]["mode"] != "tunnel" || got[1]["method"] != "CONNECT" || got[1]["bytes"] != float64(1024) {
		t.Errorf("tunnel line: %v", got[1])
	}
	if _, has := got[2]["target"]; has || got[2]["error"] != "bad_target" {
		t.Errorf("a request without a target carries no target: %v", got[2])
	}
	if strings.Contains(out.String(), "front.example.com") {
		t.Fatalf("client host leaked: %s", out.String())
	}
}

func TestUpstreamErrorsAreDeduplicated(t *testing.T) {
	l, out := newLogger(slog.LevelWarn)
	for i := 0; i < 4; i++ {
		l.Finished(directReq(metrics.ModeHeader, metrics.UpstreamTimeout))
	}
	got := lines(t, out)
	if len(got) != 1 || got[0]["msg"] != "upstream error" || got[0]["level"] != "WARN" ||
		got[0]["mode"] != "header" || got[0]["reason"] != "timeout" {
		t.Fatalf("first occurrence only:\n%s", out.String())
	}
	if len(got[0]) != 5 { // time, level, msg, mode, reason: nothing about the request
		t.Fatalf("an error line carries the reason only: %v", got[0])
	}

	// The same reason in another mode is its own series.
	l.Finished(directReq(metrics.ModeTunnel, metrics.UpstreamTimeout))
	if n := len(lines(t, out)); n != 2 {
		t.Fatalf("tunnel first occurrence missing:\n%s", out.String())
	}

	l.Flush()
	got = lines(t, out)
	if len(got) != 3 || got[2]["msg"] != "upstream errors repeated" || got[2]["mode"] != "header" ||
		got[2]["reason"] != "timeout" || got[2]["count"] != float64(3) {
		t.Fatalf("periodic count line:\n%s", out.String())
	}

	// A quiet interval forgets the reason: the next failure is logged at once again.
	l.Flush()
	l.Finished(directReq(metrics.ModeHeader, metrics.UpstreamTimeout))
	if got = lines(t, out); len(got) != 4 || got[3]["msg"] != "upstream error" {
		t.Fatalf("after a quiet interval:\n%s", out.String())
	}
}

func TestUpstreamErrorLevels(t *testing.T) {
	l, out := newLogger(slog.LevelError)
	l.Finished(directReq(metrics.ModeHeader, metrics.UpstreamTimeout))
	l.Finished(directReq(metrics.ModeHeader, metrics.UpstreamBlocked))
	wantNoLines(t, out) // warn-level reasons are hidden at error
	l.Finished(directReq(metrics.ModeHeader, metrics.UpstreamOther))
	got := lines(t, out)
	if len(got) != 1 || got[0]["level"] != "ERROR" || got[0]["reason"] != "other" {
		t.Fatalf("transport fault at error level:\n%s", out.String())
	}
}

func TestClientCancelIsNotAnUpstreamError(t *testing.T) {
	l, out := newLogger(slog.LevelInfo)
	r := directReq(metrics.ModeHeader, metrics.UpstreamCanceled)
	r.Status = 0
	l.Finished(r)
	l.Flush()
	wantNoLines(t, out)
}

// Run flushes on every injected tick and once more when its context ends; it returns,
// so the goroutine cannot leak.
func TestRunFlushesOnTicksAndStops(t *testing.T) {
	l, out := newLogger(slog.LevelInfo)
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.Run(ctx, ticks)
	}()

	l.Finished(routeReq(0, 200, time.Millisecond))
	ticks <- time.Time{} // returns once Run took the tick; its Flush completes before the next select
	l.Finished(routeReq(1, 200, time.Millisecond))
	cancel() // the final flush picks up the partial interval
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop on context cancel")
	}
	got := lines(t, out)
	if len(got) != 2 || got[0]["route"] != float64(0) || got[1]["route"] != float64(1) {
		t.Fatalf("want one summary per flush:\n%s", out.String())
	}
}

func TestSummaryIntervalIsOneMinute(t *testing.T) {
	if reqlog.SummaryInterval != time.Minute {
		t.Fatalf("SummaryInterval = %v", reqlog.SummaryInterval)
	}
}
