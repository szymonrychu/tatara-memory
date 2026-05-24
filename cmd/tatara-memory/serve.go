package main

import (
	"errors"
	"net"
	"net/http"
)

// newListener creates a TCP listener on addr. Using ":0" lets the OS pick a free port.
func newListener(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

// serve starts srv on ln. It returns nil when Shutdown is called (ErrServerClosed
// is swallowed) and propagates any other listen error.
func serve(srv *http.Server, ln net.Listener) error {
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
