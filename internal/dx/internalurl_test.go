// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package dx

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// The long requests (dxFileTransfer, data module) go to InternalURL; an unreachable InternalURL falls back to BaseURL.
func TestInternalURLRoutesLongRequests(t *testing.T) {
	var public, internal atomic.Int32
	ok := func(n *atomic.Int32) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			n.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":"ok","results":[{"absolutePath":"/x","status":true}]}`))
		}
	}
	pub := httptest.NewServer(ok(&public))
	defer pub.Close()
	in := httptest.NewServer(ok(&internal))
	defer in.Close()

	c, err := NewClient(Config{BaseURL: pub.URL, InternalURL: in.URL, Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Internal() {
		t.Fatal("Internal() should be true")
	}
	if err := c.FileTransferCreateDir(context.Background(), "s"); err != nil {
		t.Fatal(err)
	}
	if internal.Load() != 1 || public.Load() != 0 {
		t.Fatalf("createDir went to internal=%d public=%d, want 1/0", internal.Load(), public.Load())
	}
	if c.long.lockID() != c.lockID() {
		t.Errorf("the internal client must share the lock of the public one: %q vs %q", c.long.lockID(), c.lockID())
	}

	// an InternalURL nobody listens on: the request goes through BaseURL
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := "http://" + l.Addr().String()
	_ = l.Close()
	c2, _ := NewClient(Config{BaseURL: pub.URL, InternalURL: dead, Username: "u", Password: "p"})
	if err := c2.FileTransferCreateDir(context.Background(), "s"); err != nil {
		t.Fatal(err)
	}
	if public.Load() != 1 {
		t.Fatalf("fallback to BaseURL: public=%d, want 1", public.Load())
	}

	// InternalURL equal to BaseURL, or empty: no second client
	c3, _ := NewClient(Config{BaseURL: pub.URL, InternalURL: pub.URL + "/"})
	if c3.Internal() {
		t.Error("InternalURL == BaseURL must not create a second client")
	}
}

func TestUnreachable(t *testing.T) {
	if unreachable(nil) {
		t.Error("nil is not unreachable")
	}
	if !unreachable(&net.DNSError{Err: "no such host", Name: "x"}) {
		t.Error("DNS errors are unreachable")
	}
	if !unreachable(&net.OpError{Op: "dial", Err: &net.AddrError{Err: "refused"}}) {
		t.Error("dial errors are unreachable")
	}
	if unreachable(ErrCutByProxy) {
		t.Error("a cut request is an answer, not unreachable")
	}
}
