package main

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHostAllowedMatchesExactAndWildcard(t *testing.T) {
	allowed := []string{"api.anthropic.com", "*.internal.example.com", "Proxy.GOLang.org"}
	cases := map[string]bool{
		"api.anthropic.com":        true,
		"API.Anthropic.COM":        true,
		"api.anthropic.com.":       true,
		"evil-anthropic.com":       false,
		"npm.internal.example.com": true,
		"internal.example.com":     false,
		"notinternal.example.com":  false,
		"proxy.golang.org":         true,
		"":                         false,
		"registry.npmjs.org":       false,
	}
	for host, want := range cases {
		if got := hostAllowed(host, allowed); got != want {
			t.Fatalf("hostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestProxyAllowsConnectAndForwardsBytes(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("EGRESS-OK"))
	}))
	defer stub.Close()
	oldDial := proxyDialFunc
	proxyDialFunc = func(host, port string) (net.Conn, error) {
		return net.DialTimeout("tcp", net.JoinHostPort(host, port), 10*time.Second)
	}
	t.Cleanup(func() { proxyDialFunc = oldDial })
	p := &egressProxy{allowed: egressList("claude", []string{"127.0.0.1"})}
	if err := p.listen("tcp", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer p.close()
	conn, err := net.DialTimeout("tcp", p.addr(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	host := strings.TrimPrefix(stub.URL, "http://")
	if _, err := conn.Write([]byte("CONNECT " + host + " HTTP/1.1\r\nHost: " + host + "\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "HTTP/1.1 200") {
		t.Fatalf("connect denied: %q", line)
	}
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	req, _ := http.NewRequest("GET", "/", nil)
	if err := req.Write(conn); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "EGRESS-OK" {
		t.Fatalf("tunneled body = %q", body)
	}
	if len(p.denials()) != 0 {
		t.Fatalf("unexpected denials %v", p.denials())
	}
}

func TestProxyDeniesAndRecordsHost(t *testing.T) {
	p := &egressProxy{allowed: egressList("", nil)}
	if err := p.listen("tcp", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer p.close()
	conn, err := net.DialTimeout("tcp", p.addr(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("CONNECT evil.example:443 HTTP/1.1\r\nHost: evil.example:443\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "HTTP/1.1 403") {
		t.Fatalf("denied connect got %q", line)
	}
	if denied := p.denials(); len(denied) != 1 || denied[0] != "evil.example" {
		t.Fatalf("denials = %v", denied)
	}
}

func TestProxyCheckAnswersOnUnixSocket(t *testing.T) {
	dir, err := newSocketDir()
	if err != nil {
		t.Fatal(err)
	}
	defer removeTree(dir)
	if path := proxySocketPath(dir); len(path) > 100 {
		t.Skipf("socket path %q is too long for this host", path)
	}
	p := &egressProxy{allowed: egressList("", nil)}
	if err := p.listen("unix", proxySocketPath(dir)); err != nil {
		t.Fatal(err)
	}
	defer p.close()
	if err := p.check(); err != nil {
		t.Fatalf("check: %v", err)
	}
	if denied := p.denials(); len(denied) != 0 {
		t.Fatalf("preflight recorded a denial: %v", denied)
	}
}

func TestProxyCheckFailsWhenClosed(t *testing.T) {
	p := &egressProxy{allowed: egressList("", nil)}
	if err := p.check(); err == nil || !strings.Contains(err.Error(), "not listening") {
		t.Fatalf("a proxy with no listener must say so in its check: %v", err)
	}
}

func TestProxyRefusesALoopbackTarget(t *testing.T) {
	p := &egressProxy{allowed: []string{"localhost"}}
	if err := p.listen("tcp", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer p.close()
	conn, err := net.DialTimeout("tcp", p.addr(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("CONNECT localhost:8080 HTTP/1.1\r\nHost: localhost:8080\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "HTTP/1.1 403") {
		t.Fatalf("a loopback target must be refused: %q", line)
	}
	if denied := p.denials(); len(denied) != 1 || denied[0] != "localhost" {
		t.Fatalf("the refusal must be recorded: %v", denied)
	}
}

func TestAConnectionRegisteredAfterCloseIsClosed(t *testing.T) {
	p := &egressProxy{}
	p.close()
	client, server := net.Pipe()
	defer client.Close()
	if p.track(server) {
		t.Fatal("a closed proxy accepted a new connection")
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("the late connection must be closed at once: %v", err)
	}
}

func TestAHostWithAnyPrivateAnswerIsRefused(t *testing.T) {
	old := resolveHost
	defer func() { resolveHost = old }()
	resolveHost = func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("192.168.1.10")}, nil
	}
	if conn, err := vettedDial("mixed.example.test", "443"); err != errRefusedAddress {
		if conn != nil {
			conn.Close()
		}
		t.Fatalf("a name that also resolves to a private address must be refused, got %v", err)
	}
}
