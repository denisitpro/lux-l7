// Package reqlog is the request log of lux-l7 (#54): a metrics.Sink that turns request
// outcomes into JSON log lines on the standard LOG_LEVEL. There is no mode switch
// beyond the level; the proxy mode decides what each level emits.
//
// Header mode, path mode and tunnel (the log is a leak risk, the client names the target):
//   - info: nothing per request. Never client IP, path, query, target host or token.
//   - warn, error: upstream failures as a reason only, deduplicated: the first
//     occurrence is logged at once, repeats are counted into one line per interval.
//   - debug: one line per request (method, target host, status, duration, bytes).
//
// Route mode (the operator must see how the configured upstreams behave):
//   - info: one summary per interval for every route that saw traffic; silent when idle.
//   - warn: an upstream turning failing (FailingAfter consecutive errors) or
//     recovering, once each; repeats are collapsed.
//   - debug: one line per request (route, upstream, status, duration).
//
// No line ever carries the client address, path, query or credentials.
package reqlog

import (
	"context"
	"log/slog"
	"math"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/denisitpro/lux-l7/internal/metrics"
)

const (
	// SummaryInterval is how often the periodic lines (route summaries, repeated
	// upstream error counts) are written.
	SummaryInterval = time.Minute
	// FailingAfter is the number of consecutive upstream errors that turn a route failing.
	FailingAfter = 5
	// sampleCap bounds the latency samples kept per route and interval.
	sampleCap = 4096
)

// Logger is the request log. It implements metrics.Sink; Run drives its periodic lines.
type Logger struct {
	log *slog.Logger

	mu     sync.Mutex
	errs   map[errKey]*errState // header and tunnel upstream errors being deduplicated
	routes map[int]*routeState  // route mode, by route index
}

type errKey struct {
	mode   metrics.Mode
	reason metrics.UpstreamError
}

// errState is a reason already reported once; repeats since the last Flush are counted.
type errState struct{ repeats int }

// routeState is the health of one route plus the traffic of the current interval.
type routeState struct {
	upstream    string
	consecutive int // upstream errors in a row
	failing     bool

	requests int
	classes  map[string]int
	errors   map[metrics.UpstreamError]int
	latency  []time.Duration // a ring of the latest sampleCap requests
	seen     int
}

// New returns a Logger writing to log (the default logger when nil).
func New(log *slog.Logger) *Logger {
	if log == nil {
		log = slog.Default()
	}
	return &Logger{log: log, errs: map[errKey]*errState{}, routes: map[int]*routeState{}}
}

// Opened is a no-op: a tunnel is logged when it ends, with its outcome.
func (l *Logger) Opened(metrics.Request) {}

// Finished logs or records one finished request according to its mode and the level.
func (l *Logger) Finished(r metrics.Request) {
	if r.Mode == metrics.ModeRoute {
		l.routeFinished(r)
		return
	}
	if l.log.Enabled(context.Background(), slog.LevelDebug) {
		attrs := []any{"mode", string(r.Mode), "method", r.Method}
		if r.Authority != "" {
			attrs = append(attrs, "target", r.Authority)
		}
		l.log.Debug("request", append(attrs, outcome(r)...)...)
	}
	if r.UpstreamError != "" && r.UpstreamError != metrics.UpstreamCanceled {
		l.upstreamError(r)
	}
}

// outcome is the debug tail every mode shares.
func outcome(r metrics.Request) []any {
	attrs := []any{"status", r.Status, "duration_ms", ms(r.Duration), "bytes", r.Bytes}
	switch {
	case r.Rejection != "":
		attrs = append(attrs, "error", string(r.Rejection))
	case r.UpstreamError != "":
		attrs = append(attrs, "error", string(r.UpstreamError))
	}
	return attrs
}

// upstreamError logs the first failure of a reason at once and counts the repeats.
func (l *Logger) upstreamError(r metrics.Request) {
	key := errKey{r.Mode, r.UpstreamError}
	l.mu.Lock()
	st, seen := l.errs[key]
	if seen {
		st.repeats++
	} else {
		l.errs[key] = &errState{}
	}
	l.mu.Unlock()
	if !seen {
		l.log.Log(context.Background(), errLevel(r.UpstreamError), "upstream error",
			"mode", string(r.Mode), "reason", string(r.UpstreamError))
	}
}

// errLevel: a timeout or a blocked address is expected in operation (warn), anything
// else is a transport fault (error).
func errLevel(e metrics.UpstreamError) slog.Level {
	if e == metrics.UpstreamTimeout || e == metrics.UpstreamBlocked {
		return slog.LevelWarn
	}
	return slog.LevelError
}

func (l *Logger) routeFinished(r metrics.Request) {
	if r.Route < 0 {
		return // no route matched: nothing to attribute
	}
	ctx := context.Background()
	if l.log.Enabled(ctx, slog.LevelDebug) {
		l.log.Debug("request", append([]any{"mode", string(r.Mode), "route", r.Route, "upstream", r.UpstreamHost}, outcome(r)...)...)
	}

	l.mu.Lock()
	st := l.routes[r.Route]
	if st == nil {
		st = &routeState{upstream: r.UpstreamHost}
		l.routes[r.Route] = st
	}
	if l.log.Enabled(ctx, slog.LevelInfo) {
		st.record(r)
	}
	transition := st.transition(r)
	l.mu.Unlock()

	switch transition.kind {
	case failing:
		l.log.Warn("upstream failing", "route", r.Route, "upstream", r.UpstreamHost,
			"consecutive_errors", transition.errors, "reason", string(r.UpstreamError))
	case recovered:
		l.log.Warn("upstream recovered", "route", r.Route, "upstream", r.UpstreamHost,
			"consecutive_errors", transition.errors)
	}
}

// record adds r to the interval traffic.
func (st *routeState) record(r metrics.Request) {
	if st.classes == nil {
		st.classes = map[string]int{}
		st.errors = map[metrics.UpstreamError]int{}
	}
	st.requests++
	st.classes[r.Class()]++
	if r.UpstreamError != "" {
		st.errors[r.UpstreamError]++
	}
	if len(st.latency) < sampleCap {
		st.latency = append(st.latency, r.Duration)
	} else {
		st.latency[st.seen%sampleCap] = r.Duration
	}
	st.seen++
}

type transitionKind int

const (
	failing transitionKind = iota + 1
	recovered
)

type transition struct {
	kind   transitionKind
	errors int
}

// transition updates the health of the route. Only an upstream error (a client
// cancel is not one) or a response from the upstream moves it; a refusal before the
// upstream (loop, body limit) is neutral.
func (st *routeState) transition(r metrics.Request) transition {
	switch {
	case r.UpstreamError != "" && r.UpstreamError != metrics.UpstreamCanceled:
		st.consecutive++
		if !st.failing && st.consecutive >= FailingAfter {
			st.failing = true
			return transition{failing, st.consecutive}
		}
	case r.UpstreamError == "" && r.Rejection == "" && r.Status != 0:
		n := st.consecutive
		st.consecutive = 0
		if st.failing {
			st.failing = false
			return transition{recovered, n}
		}
	}
	return transition{}
}

// Flush writes the periodic lines now and starts a new interval: one summary per
// route with traffic, and one count line per upstream error reason that repeated.
// A reason that did not repeat is forgotten, so its next failure is logged at once.
func (l *Logger) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()

	keys := make([]errKey, 0, len(l.errs))
	for k := range l.errs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].mode != keys[j].mode {
			return keys[i].mode < keys[j].mode
		}
		return keys[i].reason < keys[j].reason
	})
	for _, k := range keys {
		st := l.errs[k]
		if st.repeats == 0 {
			delete(l.errs, k)
			continue
		}
		l.log.Log(context.Background(), errLevel(k.reason), "upstream errors repeated",
			"mode", string(k.mode), "reason", string(k.reason), "count", st.repeats)
		st.repeats = 0
	}

	idxs := make([]int, 0, len(l.routes))
	for i := range l.routes {
		idxs = append(idxs, i)
	}
	slices.Sort(idxs)
	for _, i := range idxs {
		st := l.routes[i]
		if st.requests == 0 {
			continue
		}
		l.log.Info("route summary", st.summary(i)...)
		st.requests, st.seen, st.latency = 0, 0, st.latency[:0]
		clear(st.classes)
		clear(st.errors)
	}
}

func (st *routeState) summary(idx int) []any {
	attrs := []any{"route", idx, "upstream", st.upstream, "requests", st.requests, slog.Group("status", countAttrs(st.classes)...)}
	if len(st.errors) > 0 {
		reasons := make(map[string]int, len(st.errors))
		for k, v := range st.errors {
			reasons[string(k)] = v
		}
		attrs = append(attrs, slog.Group("errors", countAttrs(reasons)...))
	}
	sorted := slices.Clone(st.latency)
	slices.Sort(sorted)
	return append(attrs, "p50_ms", ms(percentile(sorted, 0.50)), "p95_ms", ms(percentile(sorted, 0.95)))
}

// countAttrs lists the non-zero counts in key order.
func countAttrs(counts map[string]int) []any {
	keys := make([]string, 0, len(counts))
	for k, v := range counts {
		if v > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	attrs := make([]any, 0, len(keys))
	for _, k := range keys {
		attrs = append(attrs, slog.Int(k, counts[k]))
	}
	return attrs
}

// percentile is the nearest-rank percentile of an ascending slice.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(rank, 0)]
}

// ms is d in milliseconds with microsecond precision.
func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// Run writes the periodic lines on every tick until ctx ends, then once more so the
// last partial interval is not lost. It returns when done; callers wait for it.
func (l *Logger) Run(ctx context.Context, ticks <-chan time.Time) {
	for {
		select {
		case <-ticks:
			l.Flush()
		case <-ctx.Done():
			l.Flush()
			return
		}
	}
}
