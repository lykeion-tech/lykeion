package testutil

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Upstream serves h over HTTPS and returns a client that sends every request,
// whatever its host, to it. Tests of outbound fetches therefore use real
// catalogue hosts and never touch the network.
func Upstream(t *testing.T, h http.Handler) *http.Client {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.InsecureSkipVerify = true // the stub's certificate cannot name every catalogue host
	addr := srv.Listener.Addr().String()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	return &http.Client{Transport: tr}
}
