package metrics

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Mode is the proxy mode label.
type Mode string

// Mode values.
const (
	ModeHeader Mode = "header"
	ModeRoute  Mode = "route"
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

// Options configures New.
type Options struct {
	// Location becomes a constant label on every lux_l7_ series; empty adds none.
	Location string
	// IngressHosts are the front hosts header mode may label; every other host is "other".
	IngressHosts []string
}

// Metrics records proxy request series.
type Metrics struct {
	hosts          Hosts
	requests       *prometheus.CounterVec
	duration       *prometheus.HistogramVec
	rejections     *prometheus.CounterVec
	upstreamErrors *prometheus.CounterVec
	tunnelsActive  prometheus.Gauge
}

// New registers the series on reg.
func New(reg prometheus.Registerer, opts Options) *Metrics {
	if opts.Location != "" {
		reg = prometheus.WrapRegistererWith(prometheus.Labels{"location": opts.Location}, reg)
	}
	m := &Metrics{
		hosts: NewHosts(opts.IngressHosts),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "requests_total",
			Help: "Proxied requests by mode, ingress host, upstream host and status class (2xx|3xx|4xx|5xx|error); rejections included.",
		}, []string{"mode", "ingress_host", "upstream_host", "status_class"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "request_duration_seconds",
			Help:    "Request latency by mode and status class; for a tunnel, its lifetime.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
		}, []string{"mode", "status_class"}),
		rejections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "rejections_total",
			Help: "Requests refused before reaching an upstream, by mode and reason.",
		}, []string{"mode", "reason"}),
		upstreamErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "upstream_errors_total",
			Help: "Failed upstream round trips and dials by mode and reason (timeout|blocked|canceled|other).",
		}, []string{"mode", "reason"}),
		tunnelsActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Name: "tunnels_active",
			Help: "Open CONNECT tunnels.",
		}),
	}
	reg.MustRegister(m.requests, m.duration, m.rejections, m.upstreamErrors, m.tunnelsActive)
	return m
}

// Begin starts observing one request and returns the writer the handler must use
// from then on. A nil *Metrics returns w itself and a nil *Observation.
func (m *Metrics) Begin(mode Mode, w http.ResponseWriter) (http.ResponseWriter, *Observation) {
	if m == nil {
		return w, nil
	}
	o := &Observation{ResponseWriter: w, m: m, mode: mode, start: time.Now(), ingress: Other, upstream: Other}
	return o, o
}

// Observation is one request in flight. It is also the http.ResponseWriter that
// captures the status. All methods are safe on a nil receiver.
type Observation struct {
	http.ResponseWriter
	m        *Metrics
	mode     Mode
	start    time.Time
	status   int
	ingress  string
	upstream string
	counted  bool
	tunnel   bool
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

// Write records the implicit 200.
func (o *Observation) Write(b []byte) (int, error) {
	if o.status == 0 {
		o.status = http.StatusOK
	}
	return o.ResponseWriter.Write(b)
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

// Ingress labels the request by a client-supplied Host (header mode).
func (o *Observation) Ingress(requestHost string) {
	if o != nil {
		o.ingress = o.m.hosts.Ingress(requestHost)
	}
}

// Route labels the request by the matched route (route mode).
func (o *Observation) Route(hostPattern, upstreamHost string) {
	if o != nil {
		o.ingress = o.m.hosts.RouteIngress(hostPattern)
		o.upstream = o.m.hosts.RouteUpstream(upstreamHost)
	}
}

// Upstream labels the request by the allow-list entry that admits authority
// (header mode and tunnel). The matcher is not called when metrics are off.
func (o *Observation) Upstream(p EntryMatcher, authority string) {
	if o != nil {
		o.upstream = o.m.hosts.Upstream(p.AllowEntry(authority))
	}
}

// Reject records a refused request.
func (o *Observation) Reject(r Rejection) {
	if o != nil {
		o.m.rejections.WithLabelValues(string(o.mode), string(r)).Inc()
	}
}

// UpstreamError records a failed upstream round trip or dial.
func (o *Observation) UpstreamError(e UpstreamError) {
	if o != nil {
		o.m.upstreamErrors.WithLabelValues(string(o.mode), string(e)).Inc()
	}
}

// SetStatus records a status written outside the ResponseWriter (a hijacked connection).
func (o *Observation) SetStatus(code int) {
	if o != nil {
		o.status = code
	}
}

// TunnelOpen marks an established tunnel: it counts the request now (a tunnel can
// outlive any scrape window) and raises tunnels_active until Done.
func (o *Observation) TunnelOpen() {
	if o == nil {
		return
	}
	o.status = http.StatusOK
	o.tunnel = true
	o.m.tunnelsActive.Inc()
	o.count()
}

// Done finishes the request: counts it (unless TunnelOpen did) and records its duration.
func (o *Observation) Done() {
	if o == nil {
		return
	}
	if o.tunnel {
		o.m.tunnelsActive.Dec()
	}
	o.count()
	o.m.duration.WithLabelValues(string(o.mode), o.class()).Observe(time.Since(o.start).Seconds())
}

func (o *Observation) count() {
	if o.counted {
		return
	}
	o.counted = true
	o.m.requests.WithLabelValues(string(o.mode), o.ingress, o.upstream, o.class()).Inc()
}

// class is the coarse status class; no recorded status means no response was written.
func (o *Observation) class() string {
	switch {
	case o.status == 0:
		return statusError
	case o.status < 300:
		return "2xx"
	case o.status < 400:
		return "3xx"
	case o.status < 500:
		return "4xx"
	default:
		return "5xx"
	}
}
