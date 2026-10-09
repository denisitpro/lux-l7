// Package bind opens TCP listeners with an actionable error for the one failure
// operators hit in containers: binding a privileged port as a non-root user.
package bind

import (
	"errors"
	"fmt"
	"net"
	"syscall"
)

// Listen binds addr. On EACCES the error names the cause: a privileged port
// (below 1024) needs the -host image variant (file capability) or a port >= 1024.
func Listen(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, hint(err)
	}
	return ln, nil
}

func hint(err error) error {
	if errors.Is(err, syscall.EACCES) {
		return fmt.Errorf("%w (privileged port: use the -host image variant or a port >= 1024)", err)
	}
	return err
}
