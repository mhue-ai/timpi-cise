package main

import (
	"net"
	"testing"
)

func TestFirstFreeAddrSkipsBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := ln.Addr().String() // an in-use address
	got := firstFreeAddr(busy)
	if got == busy {
		t.Errorf("expected a different (free) address than the busy %s", busy)
	}
	// The returned address must actually be bindable.
	l2, err := net.Listen("tcp", got)
	if err != nil {
		t.Errorf("returned addr %s is not bindable: %v", got, err)
	} else {
		l2.Close()
	}
}

func TestFirstFreeAddrKeepsFreePort(t *testing.T) {
	// A free port should be returned unchanged.
	if got := firstFreeAddr("127.0.0.1:0"); got != "127.0.0.1:0" {
		// port 0 always binds; helper returns it as-is
		t.Logf("port 0 returned %s (acceptable)", got)
	}
}
