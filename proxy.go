package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type egressProxy struct {
	listener net.Listener
	allowed  []string
	quiet    bool
	mu       sync.Mutex
	denied   []string
	conns    []net.Conn
	closed   bool
}

func (p *egressProxy) track(conn net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		conn.Close()
		return false
	}
	p.conns = append(p.conns, conn)
	return true
}

func (p *egressProxy) untrack(conn net.Conn) {
	p.mu.Lock()
	for i, c := range p.conns {
		if c == conn {
			p.conns = append(p.conns[:i], p.conns[i+1:]...)
			break
		}
	}
	p.mu.Unlock()
}

func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return false
	}
	for _, entry := range allowed {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, "*.") {
			if strings.HasSuffix(host, entry[1:]) && len(host) > len(entry)-1 {
				return true
			}
			continue
		}
		if host == entry {
			return true
		}
	}
	return false
}

func (p *egressProxy) listen(network, addr string) error {
	ln, err := net.Listen(network, addr)
	if err != nil {
		return err
	}
	p.listener = ln
	go p.serve()
	return nil
}

func (p *egressProxy) close() {
	if p.listener != nil {
		p.listener.Close()
	}
	p.mu.Lock()
	p.closed = true
	for _, conn := range p.conns {
		conn.Close()
	}
	p.conns = nil
	p.mu.Unlock()
}

func (p *egressProxy) addr() string {
	if p.listener == nil {
		return ""
	}
	return p.listener.Addr().String()
}

func (p *egressProxy) serve() {
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			return
		}
		if !p.track(conn) {
			continue
		}
		go p.handle(conn)
	}
}

func (p *egressProxy) handle(conn net.Conn) {
	defer conn.Close()
	defer p.untrack(conn)
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		p.denyRequest(conn, "")
		return
	}
	target := req.URL.Host
	if target == "" {
		target = req.Host
	}
	if req.Method != "CONNECT" {
		p.denyRequest(conn, req.Host)
		return
	}
	host, port, splitErr := net.SplitHostPort(withDefaultPort(target))
	if splitErr != nil {
		host = strings.TrimSpace(target)
		port = "443"
	}
	if !hostAllowed(host, p.allowed) {
		p.denyRequest(conn, host)
		return
	}
	_ = conn.SetDeadline(time.Time{})
	out, err := proxyDialFunc(host, port)
	if err != nil {
		if errors.Is(err, errRefusedAddress) {
			p.denyRequest(conn, host)
			return
		}
		fmt.Fprintf(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer out.Close()
	defer p.untrack(out)
	if !p.track(out) {
		return
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(out, reader)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(conn, out)
		done <- struct{}{}
	}()
	<-done
}

var proxyDialFunc = vettedDial

var errRefusedAddress = errors.New("the host resolves only to private, loopback, link-local or multicast addresses")

func vettedDial(host, port string) (net.Conn, error) {
	if ip := net.ParseIP(host); ip != nil {
		if refusedIP(ip) {
			return nil, errRefusedAddress
		}
		return net.DialTimeout("tcp", net.JoinHostPort(host, port), 30*time.Second)
	}
	ips, err := resolveHost(host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errRefusedAddress
	}
	for _, ip := range ips {
		if refusedIP(ip) {
			return nil, errRefusedAddress
		}
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), port), 30*time.Second)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

var resolveHost = lookupHost

func lookupHost(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, addr := range addrs {
		ips = append(ips, addr.IP)
	}
	return ips, nil
}

var refusedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("255.255.255.255/32"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("2002::/16"),
}

func refusedIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() ||
		ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	for _, prefix := range refusedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func withDefaultPort(hostPort string) string {
	if _, _, err := net.SplitHostPort(hostPort); err != nil {
		return net.JoinHostPort(hostPort, "443")
	}
	return hostPort
}

const deniedHostLimit = 10

var deniedHostGrammar = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)

func (p *egressProxy) denyRequest(conn net.Conn, host string) {
	if host != "" && deniedHostGrammar.MatchString(host) {
		p.mu.Lock()
		quiet := p.quiet
		if !quiet && !containsFold(p.denied, host) && len(p.denied) < deniedHostLimit {
			p.denied = append(p.denied, host)
		}
		p.mu.Unlock()
	}
	fmt.Fprintf(conn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
}

func containsFold(list []string, s string) bool {
	for _, item := range list {
		if strings.EqualFold(item, s) {
			return true
		}
	}
	return false
}

func (p *egressProxy) denials() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	denied := append([]string{}, p.denied...)
	sort.Strings(denied)
	return denied
}

func (p *egressProxy) check() error {
	addr := p.addr()
	if addr == "" {
		return fmt.Errorf("the proxy is not listening")
	}
	var conn net.Conn
	var err error
	if strings.HasPrefix(addr, "/") {
		conn, err = net.DialTimeout("unix", addr, 5*time.Second)
	} else {
		conn, err = net.DialTimeout("tcp", addr, 5*time.Second)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	p.mu.Lock()
	p.quiet = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.quiet = false
		p.mu.Unlock()
	}()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(conn, "CONNECT ghafk-preflight.invalid:443 HTTP/1.1\r\nHost: ghafk-preflight.invalid:443\r\n\r\n")
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return fmt.Errorf("no answer: %v", err)
	}
	if !strings.HasPrefix(line, "HTTP/1.1 4") {
		return fmt.Errorf("unexpected answer %q", strings.TrimSpace(line))
	}
	return nil
}

func proxySocketPath(runDir string) string {
	return runDir + string(os.PathSeparator) + "proxy.sock"
}
