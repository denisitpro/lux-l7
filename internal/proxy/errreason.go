package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/denisitpro/lux-l7/internal/egress"
	"github.com/denisitpro/lux-l7/internal/metrics"
)

// ObserveUpstreamError records err on o with the same precedence as the branches of
// WriteUpstreamError; keep the two in step. A body cut by the size limit is the
// client's rejection (413 body_too_large), every other failure an upstream error.
func ObserveUpstreamError(o *metrics.Observation, err error) {
	var netErr net.Error
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		o.Reject(metrics.RejectBodyTooLarge)
	case errors.Is(err, egress.ErrBlockedAddress):
		o.UpstreamError(metrics.UpstreamBlocked)
	case errors.Is(err, context.Canceled):
		o.UpstreamError(metrics.UpstreamCanceled)
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		o.UpstreamError(metrics.UpstreamTimeout)
	default:
		o.UpstreamError(metrics.UpstreamOther)
	}
}
