package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Options configures New.
type Options struct {
	// Location becomes a constant label on every lux_l7_ series; empty adds none.
	Location string
	// IngressHosts are the front hosts header mode may label; every other host is "other".
	IngressHosts []string
}

// Metrics is the Prometheus Sink: it records proxy request series.
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

// Opened counts an established tunnel now (it can outlive any scrape window) and
// raises tunnels_active until Finished.
func (m *Metrics) Opened(r Request) {
	m.tunnelsActive.Inc()
	m.count(r)
}

// Finished counts the request (unless Opened did), records its duration and any
// rejection or upstream error.
func (m *Metrics) Finished(r Request) {
	if r.Tunnel {
		m.tunnelsActive.Dec()
	} else {
		m.count(r)
	}
	if r.Rejection != "" {
		m.rejections.WithLabelValues(string(r.Mode), string(r.Rejection)).Inc()
	}
	if r.UpstreamError != "" {
		m.upstreamErrors.WithLabelValues(string(r.Mode), string(r.UpstreamError)).Inc()
	}
	m.duration.WithLabelValues(string(r.Mode), r.Class()).Observe(r.Duration.Seconds())
}

// count adds the request to requests_total; the matcher behind AllowEntry runs here only.
func (m *Metrics) count(r Request) {
	var ingress, upstream string
	if r.Route >= 0 {
		ingress, upstream = m.hosts.RouteIngress(r.RouteHost), m.hosts.RouteUpstream(r.UpstreamHost)
	} else {
		ingress, upstream = m.hosts.Ingress(r.IngressHost), Other
		if r.Authority != "" {
			upstream = m.hosts.Upstream(r.AllowEntry())
		}
	}
	m.requests.WithLabelValues(string(r.Mode), ingress, upstream, r.Class()).Inc()
}
