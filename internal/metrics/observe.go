package metrics

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"time"
)

// Mode is the proxy mode label.
type Mode string

// Mode values.
const (
	ModeHeader Mode = "header"
	ModeRoute  Mode = "route"
	ModePath   Mode = "path"
	ModeTunnel Mode = "tunnel"
)

// Rejection is a request refused before it reached an upstream. The set is fixed and
// mirrors the JSON error codes (the header-mode forbidden code is split by cause).
type Rejection string

// Rejection values.
const (
	RejectBadTarget        Rejection = "bad_target"
	RejectUnauthorized     Rejection = "unauthorized"
	RejectProxyAuth        Rejection = "proxy_auth_required"
	RejectForbiddenHost    Rejection = "forbidden_host"
	RejectForbiddenPort    Rejection = "forbidden_port"
	RejectBodyTooLarge     Rejection = "body_too_large"
	RejectMethodNotAllowed Rejection = "method_not_allowed"
	RejectNoRoute          Rejection = "no_route"
	RejectBadHops          Rejection = "bad_hops"
	RejectLoopDetected     Rejection = "loop_detected"
	RejectShuttingDown     Rejection = "shutting_down"
	RejectTunnelCapacity   Rejection = "tunnel_capacity"
	RejectInternal         Rejection = "internal"
)

// UpstreamError is the cause of a failed upstream round trip or dial.
type UpstreamError string

// UpstreamError values.
const (
	UpstreamTimeout  UpstreamError = "timeout"
	UpstreamBlocked  UpstreamError = "blocked"
	UpstreamCanceled UpstreamError = "canceled"
	UpstreamOther    UpstreamError = "other"
)

const statusError = "error" // no response was written

// EntryMatcher names the allow-list entry that admits a target (policy.Policy).
type EntryMatcher interface {
	AllowEntry(authority string) string
}

// Request is the outcome of one request, handed to every Sink. It carries raw
// facts (the client-supplied Host, the target authority, the route); what a sink
// may export from them, and as which label, is the sink's decision.
type Request struct {
	Mode   Mode
	Method string
	// Duration is zero when a tunnel opens and the request lifetime when it finishes.
	Duration time.Duration
	// Status is the final status; 0 means no response was written.
	Status int
	// Bytes counts response body bytes, or for a tunnel the bytes relayed both ways.
	Bytes int64
	// IngressHost is the client-supplied Host header (header mode).
	IngressHost string
	// Authority is the client-named target, host or host:port (header mode and tunnel).
	Authority string
	// Route is the matched route index, -1 when none matched or not in route mode.
	Route        int
	RouteHost    string // the matched route's host pattern as configured
	UpstreamHost string // the matched route's upstream host
	// Rejection is set when the request was refused before an upstream.
	Rejection Rejection
	// UpstreamError is set when the upstream round trip or dial failed.
	UpstreamError UpstreamError
	// Tunnel marks an established CONNECT tunnel.
	Tunnel bool

	matcher EntryMatcher
}

// AllowEntry names the allow-list entry that admitted Authority, empty when none did.
func (r Request) AllowEntry() string {
	if r.matcher == nil {
		return ""
	}
	return r.matcher.AllowEntry(r.Authority)
}

// Class is the coarse status class; no recorded status means no response was written.
func (r Request) Class() string {
	switch {
	case r.Status == 0:
		return statusError
	case r.Status < 300:
		return "2xx"
	case r.Status < 400:
		return "3xx"
	case r.Status < 500:
		return "4xx"
	default:
		return "5xx"
	}
}

// Sink consumes request outcomes: the Prometheus export (Metrics), the request log
// (reqlog.Logger). Calls come from request goroutines, so a sink must be safe for
// concurrent use and must not block.
type Sink interface {
	// Opened reports an established tunnel, which can outlive any scrape window.
	Opened(Request)
	// Finished reports the end of every request, a tunnel's close included.
	Finished(Request)
}

// Observer starts observations and fans their outcome out to its sinks. A nil
// *Observer, like a nil *Observation, records nothing.
type Observer struct {
	sinks []Sink
}

// NewObserver returns an Observer over sinks; with none it returns nil.
func NewObserver(sinks ...Sink) *Observer {
	if len(sinks) == 0 {
		return nil
	}
	return &Observer{sinks: sinks}
}

// Begin starts observing one request and returns the writer the handler must use
// from then on. A nil *Observer returns w itself and a nil *Observation.
func (ob *Observer) Begin(mode Mode, w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *Observation) {
	if ob == nil {
		return w, nil
	}
	o := &Observation{ResponseWriter: w, ob: ob, mode: mode, method: r.Method, start: time.Now(), route: -1}
	return o, o
}

// Observation is one request in flight. It is also the http.ResponseWriter that
// captures the status and size. All methods are safe on a nil receiver.
type Observation struct {
	http.ResponseWriter
	ob     *Observer
	mode   Mode
	method string
	start  time.Time
	status int
	bytes  int64

	ingressHost  string
	authority    string
	matcher      EntryMatcher
	route        int
	routeHost    string
	upstreamHost string
	rejection    Rejection
	upstreamErr  UpstreamError
	tunnel       bool
}

// From returns the Observation behind w, or nil. The reverse proxy hands its
// ErrorHandler the same writer the handler gave it, so this finds the request there.
func From(w http.ResponseWriter) *Observation {
	o, _ := w.(*Observation)
	return o
}

// WriteHeader records the first final status (101 counts; other 1xx do not).
func (o *Observation) WriteHeader(code int) {
	if o.status == 0 && (code >= 200 || code == http.StatusSwitchingProtocols) {
		o.status = code
	}
	o.ResponseWriter.WriteHeader(code)
}

// Write records the implicit 200 and the body size.
func (o *Observation) Write(b []byte) (int, error) {
	if o.status == 0 {
		o.status = http.StatusOK
	}
	n, err := o.ResponseWriter.Write(b)
	o.bytes += int64(n)
	return n, err
}

// Flush forwards to the underlying writer when it can flush.
func (o *Observation) Flush() {
	if f, ok := o.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the underlying writer; a hijacked response with no recorded
// status counts as 2xx (an upgrade), a tunnel sets its own through TunnelOpen.
func (o *Observation) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := o.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("metrics: writer cannot be hijacked")
	}
	c, rw, err := h.Hijack()
	if err == nil && o.status == 0 {
		o.status = http.StatusOK
	}
	return c, rw, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (o *Observation) Unwrap() http.ResponseWriter { return o.ResponseWriter }

// Ingress records the client-supplied Host (header mode).
func (o *Observation) Ingress(requestHost string) {
	if o != nil {
		o.ingressHost = requestHost
	}
}

// Route records the matched route (route mode): its index, host pattern and upstream host.
func (o *Observation) Route(idx int, hostPattern, upstreamHost string) {
	if o != nil {
		o.route, o.routeHost, o.upstreamHost = idx, hostPattern, upstreamHost
	}
}

// Upstream records the client-named target and the matcher that maps it to an
// allow-list entry (header mode and tunnel). The matcher runs only if a sink asks.
func (o *Observation) Upstream(p EntryMatcher, authority string) {
	if o != nil {
		o.matcher, o.authority = p, authority
	}
}

// Reject records a refused request.
func (o *Observation) Reject(r Rejection) {
	if o != nil {
		o.rejection = r
	}
}

// UpstreamError records a failed upstream round trip or dial.
func (o *Observation) UpstreamError(e UpstreamError) {
	if o != nil {
		o.upstreamErr = e
	}
}

// SetStatus records a status written outside the ResponseWriter (a hijacked connection).
func (o *Observation) SetStatus(code int) {
	if o != nil {
		o.status = code
	}
}

// AddBytes records bytes moved outside the ResponseWriter (a tunnel's relay).
func (o *Observation) AddBytes(n int64) {
	if o != nil {
		o.bytes += n
	}
}

// TunnelOpen marks an established tunnel: sinks hear Opened now (a tunnel can
// outlive any scrape window) and Finished when Done runs.
func (o *Observation) TunnelOpen() {
	if o == nil {
		return
	}
	o.status = http.StatusOK
	o.tunnel = true
	req := o.request()
	for _, s := range o.ob.sinks {
		s.Opened(req)
	}
}

// Done finishes the request and hands it to every sink.
func (o *Observation) Done() {
	if o == nil {
		return
	}
	req := o.request()
	req.Duration = time.Since(o.start)
	for _, s := range o.ob.sinks {
		s.Finished(req)
	}
}

func (o *Observation) request() Request {
	return Request{
		Mode: o.mode, Method: o.method, Status: o.status, Bytes: o.bytes,
		IngressHost: o.ingressHost, Authority: o.authority, matcher: o.matcher,
		Route: o.route, RouteHost: o.routeHost, UpstreamHost: o.upstreamHost,
		Rejection: o.rejection, UpstreamError: o.upstreamErr, Tunnel: o.tunnel,
	}
}
