package main

import (
	"net"
	"net/url"
	"strings"
	"time"
)

// dockerNetworkLLMHost is the LiteLLM host name used inside the Docker network.
const dockerNetworkLLMHost = "litellm:4000"

// hostLLMFallback is the LiteLLM address published on the development host.
const hostLLMFallback = "http://localhost:8448"

// dbNetworkHost is the PostgreSQL host name used inside the Docker network.
const dbNetworkHost = "db:5432"

// dbHostFallback is the PostgreSQL address published on the development host.
const dbHostFallback = "127.0.0.1:5432"

// resolveLLMEndpoint returns a LiteLLM base URL reachable from the current
// process. The daemon and the containerized CLI reach LiteLLM on the Docker
// network (http://litellm:4000). A CLI run directly on the host cannot resolve
// that name, so it falls back to the published port. The configured URL is kept
// when it is already reachable or is not the Docker-network host.
func resolveLLMEndpoint(configured string) string {
	return selectLLMEndpoint(configured, tcpReachable)
}

// resolveDatabaseURL keeps the Docker-network database host when it is
// reachable, and falls back to the published host port otherwise.
func resolveDatabaseURL(configured string) string {
	return selectDatabaseURL(configured, tcpReachable)
}

// selectDatabaseURL holds the database-endpoint choice logic, taking the
// reachability probe as a parameter for isolated testing.
func selectDatabaseURL(configured string, reachable func(string) bool) string {
	trimmed := strings.TrimSpace(configured)
	if trimmed == "" || !strings.Contains(trimmed, "@"+dbNetworkHost) {
		return configured
	}
	if reachable("http://" + dbNetworkHost) {
		return configured
	}
	return strings.Replace(trimmed, "@"+dbNetworkHost, "@"+dbHostFallback, 1)
}

// selectLLMEndpoint holds the endpoint-choice logic. It takes the reachability
// probe as a parameter so the branch behavior is testable in isolation.
func selectLLMEndpoint(configured string, reachable func(string) bool) string {
	trimmed := strings.TrimSpace(configured)
	if trimmed == "" || !strings.Contains(trimmed, dockerNetworkLLMHost) {
		return configured
	}
	if reachable(trimmed) {
		return configured
	}
	return hostLLMFallback
}

// tcpReachable reports whether the host and port of rawURL accept a TCP
// connection within the probe timeout.
func tcpReachable(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := u.Host
	if host == "" {
		return false
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		// No explicit port; assume the URL scheme's default.
		port := "80"
		if u.Scheme == "https" {
			port = "443"
		}
		host = net.JoinHostPort(host, port)
	}
	conn, err := net.DialTimeout("tcp", host, 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
