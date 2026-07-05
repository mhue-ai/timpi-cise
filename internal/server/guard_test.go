package server

import "testing"

func TestHostIsLocal(t *testing.T) {
	local := []string{"127.0.0.1:8770", "localhost:8770", "[::1]:8770", "127.0.0.1"}
	for _, h := range local {
		if !hostIsLocal(h) {
			t.Errorf("hostIsLocal(%q) = false, want true", h)
		}
	}
	remote := []string{"evil.com:8770", "192.168.1.20:8770", "timpi.com", "10.0.0.5:80"}
	for _, h := range remote {
		if hostIsLocal(h) {
			t.Errorf("hostIsLocal(%q) = true, want false", h)
		}
	}
}

func TestOriginMatchesHost(t *testing.T) {
	if !originMatchesHost("http://127.0.0.1:8770", "127.0.0.1:8770") {
		t.Error("same-origin should match")
	}
	if !originMatchesHost("http://localhost:8770", "127.0.0.1:8770") {
		t.Error("loopback origin should be allowed")
	}
	if originMatchesHost("https://evil.com", "127.0.0.1:8770") {
		t.Error("cross-origin should not match")
	}
}
