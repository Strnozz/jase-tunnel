package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const webProtocol = "jase-tunnel-v1"

func webOrigin(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Host == "" || u.Scheme+"://"+u.Host != value {
		return false
	}
	host := u.Hostname()
	if devBuild == "true" && u.Scheme == "http" && u.Port() != "" && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
		return true
	}
	if u.Scheme != "https" || u.Port() != "" || len(host) > 253 || host != strings.ToLower(host) || !strings.Contains(host, ".") || strings.HasSuffix(host, ".") || net.ParseIP(host) != nil {
		return false
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".home.arpa", ".test", ".invalid", ".example"} {
		if strings.HasSuffix(host, suffix) {
			return false
		}
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func webBrokerPath(value string) bool {
	if len(value) > 200 || !strings.HasPrefix(value, "/") || !strings.HasSuffix(value, "/") || strings.Contains(value, "//") {
		return false
	}
	for _, segment := range strings.Split(strings.Trim(value, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, c := range segment {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}

var deniedWebNetworks = func() []netip.Prefix {
	values := []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20"}
	out := []netip.Prefix{}
	for _, value := range values {
		out = append(out, netip.MustParsePrefix(value))
	}
	return out
}()

func publicWebIP(value net.IP) bool {
	ip, ok := netip.AddrFromSlice(value)
	if !ok {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || !ip.Is4() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, prefix := range deniedWebNetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// Validate every DNS answer and dial the verified address itself. TLS retains the
// original hostname/SNI. No environment proxy or redirect can change the destination.
var webTransport = &http.Transport{Proxy: nil, MaxResponseHeaderBytes: 8192, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 25 * time.Second,
	DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(address)
		return dialVerified(ctx, network, address, devBuild == "true" && (host == "localhost" || host == "127.0.0.1" || host == "::1"))
	},
}

// Validate every DNS answer, then dial the verified addresses in order, so a site whose
// first address is unreachable (IPv6 without connectivity, ::1 for an IPv4-only local
// server) still connects. loopback: a development build reaching a local site.
func dialVerified(ctx context.Context, network, address string, loopback bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if !loopback && port != "443" {
		return nil, errors.New("web destination rejected")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("web destination unavailable")
	}
	for _, ip := range ips {
		if !loopback && !publicWebIP(ip.IP) || loopback && !ip.IP.IsLoopback() {
			return nil, errors.New("web destination rejected")
		}
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	var failure error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		failure = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, failure
}

var webClient = &http.Client{Transport: webTransport, Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func validWebLink(c link) bool {
	return c.Protocol == webProtocol && webOrigin(c.Origin) && webBrokerPath(c.Broker) && c.ExpiresAt > time.Now().UnixMilli() && c.Model.Key != "" && len(c.Token) == 64
}

func (a *application) webHTTPClient() *http.Client {
	if a.webHTTP != nil {
		return a.webHTTP
	}
	return webClient
}
