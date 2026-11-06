package main

import (
	"net"
	"testing"
)

func TestSelectLLMEndpoint(t *testing.T) {
	reachable := true
	probe := func(string) bool { return reachable }

	tests := []struct {
		name       string
		configured string
		reachable  bool
		want       string
	}{
		{
			name:       "non litellm url is returned unchanged",
			configured: "https://openrouter.ai/api/v1",
			reachable:  false,
			want:       "https://openrouter.ai/api/v1",
		},
		{
			name:       "empty url is returned unchanged",
			configured: "",
			reachable:  false,
			want:       "",
		},
		{
			name:       "litellm url kept when reachable",
			configured: "http://litellm:4000",
			reachable:  true,
			want:       "http://litellm:4000",
		},
		{
			name:       "litellm url falls back to host port when unreachable",
			configured: "http://litellm:4000",
			reachable:  false,
			want:       hostLLMFallback,
		},
		{
			name:       "surrounding whitespace does not defeat detection",
			configured: "  http://litellm:4000  ",
			reachable:  false,
			want:       hostLLMFallback,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reachable = tt.reachable
			if got := selectLLMEndpoint(tt.configured, probe); got != tt.want {
				t.Fatalf("selectLLMEndpoint(%q, reachable=%v) = %q, want %q", tt.configured, tt.reachable, got, tt.want)
			}
		})
	}
}

// TestTCPReachable exercises the real network probe against a live listener and
// a closed port.
func TestTCPReachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	if !tcpReachable("http://" + addr) {
		t.Fatalf("expected %s to be reachable", addr)
	}

	closed := "http://127.0.0.1:1"
	if tcpReachable(closed) {
		t.Fatalf("expected %s to be unreachable", closed)
	}

	if tcpReachable("not a url") {
		t.Fatalf("expected malformed url to be unreachable")
	}
}
