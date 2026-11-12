package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestIsBlocked(t *testing.T) {
	blocked := []string{
		"0.0.0.0", "0.1.2.3",
		"10.0.0.1",
		"100.64.0.1", "100.100.100.100", "100.127.255.255",
		"127.0.0.1", "127.255.255.254",
		"169.254.169.254",
		"172.16.0.1", "172.31.255.255",
		"192.0.0.1", "192.0.2.1", "192.88.99.1",
		"192.168.1.1",
		"198.18.0.1", "198.19.255.255", "198.51.100.1", "203.0.113.1",
		"224.0.0.1", "239.255.255.250",
		"240.0.0.1", "255.255.255.255",
		"::", "::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254",
		"64:ff9b::7f00:1",    // NAT64 of 127.0.0.1
		"64:ff9b::a9fe:a9fe", // NAT64 of 169.254.169.254
		"64:ff9b:1::1",       // local-use NAT64
		"2002:7f00:1::1",     // 6to4 of 127.0.0.1
		"2002:c0a8:101::1",   // 6to4 of 192.168.1.1
		"2001::1",            // Teredo
		"2001:db8::1",
		"100::1",
		"fc00::1", "fd12:3456::1",
		"fe80::1", "fe80::1%eth0",
		"fec0::1",
		"ff02::1",
	}
	allowed := []string{
		"1.1.1.1", "8.8.8.8", "9.9.9.9",
		"100.63.255.255", "100.128.0.0", // just outside CGNAT
		"172.15.255.255", "172.32.0.0",
		"192.0.1.1", "198.17.255.255", "198.20.0.0",
		"223.255.255.255",
		"2606:4700:4700::1111", "2001:4860:4860::8888",
		"::ffff:8.8.8.8",
		"64:ff9b::808:808", // NAT64 of 8.8.8.8
		"2002:808:808::1",  // 6to4 of 8.8.8.8
	}
	for _, s := range blocked {
		if !IsBlocked(netip.MustParseAddr(s)) {
			t.Errorf("IsBlocked(%s) = false, want true", s)
		}
	}
	for _, s := range allowed {
		if IsBlocked(netip.MustParseAddr(s)) {
			t.Errorf("IsBlocked(%s) = true, want false", s)
		}
	}
	if !IsBlocked(netip.Addr{}) {
		t.Error("zero Addr should be blocked")
	}
	if !IsBlockedIP(nil) {
		t.Error("nil net.IP should be blocked")
	}
	if !IsBlockedIP(net.ParseIP("0.0.0.0")) || IsBlockedIP(net.ParseIP("8.8.8.8")) {
		t.Error("IsBlockedIP disagrees with IsBlocked")
	}
}

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := f[host]; ok {
		return a, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

var errDialed = errors.New("dialed")

func recordingDialer(r Resolver, allowed map[string]bool) (*Dialer, *string) {
	var got string
	return &Dialer{
		AllowedHosts: allowed,
		Resolver:     r,
		Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			got = addr
			return nil, errDialed
		},
	}, &got
}

func TestDialerBlocks(t *testing.T) {
	r := fakeResolver{
		"public.example":   {netip.MustParseAddr("93.184.216.34")},
		"rebind.example":   {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.5")},
		"metadata.example": {netip.MustParseAddr("169.254.169.254")},
		"mapped.example":   {netip.MustParseAddr("::ffff:127.0.0.1")},
	}
	cases := []struct {
		addr      string
		wantBlock bool
		wantDial  string
	}{
		{"public.example:443", false, "93.184.216.34:443"},
		{"rebind.example:443", true, ""},
		{"metadata.example:80", true, ""},
		{"mapped.example:80", true, ""},
		{"localhost:80", true, ""},
		{"LOCALHOST.:80", true, ""},
		{"api.localhost:80", true, ""},
		{"0.0.0.0:80", true, ""},
		{"100.64.1.2:80", true, ""},
		{"[::]:80", true, ""},
		{"[::ffff:7f00:1]:80", true, ""},
		{"8.8.8.8:53", false, "8.8.8.8:53"},
		{"[2606:4700:4700::1111]:443", false, "[2606:4700:4700::1111]:443"},
	}
	for _, c := range cases {
		d, got := recordingDialer(r, nil)
		_, err := d.DialContext(context.Background(), "tcp", c.addr)
		if c.wantBlock {
			if !errors.Is(err, ErrBlocked) {
				t.Errorf("%s: err = %v, want ErrBlocked", c.addr, err)
			}
			if *got != "" {
				t.Errorf("%s: dialed %s despite block", c.addr, *got)
			}
			continue
		}
		if !errors.Is(err, errDialed) {
			t.Errorf("%s: err = %v, want dial", c.addr, err)
		}
		if *got != c.wantDial {
			t.Errorf("%s: dialed %q, want %q", c.addr, *got, c.wantDial)
		}
	}
}

func TestDialerAllowedHosts(t *testing.T) {
	d, got := recordingDialer(fakeResolver{}, map[string]bool{"garage": true})
	if _, err := d.DialContext(context.Background(), "tcp", "Garage:3900"); !errors.Is(err, errDialed) {
		t.Fatalf("allowed host: err = %v", err)
	}
	if *got != "Garage:3900" {
		t.Fatalf("allowed host dialed %q", *got)
	}
}

func TestDialerLookupError(t *testing.T) {
	d, got := recordingDialer(fakeResolver{}, nil)
	if _, err := d.DialContext(context.Background(), "tcp", "missing.example:80"); err == nil || errors.Is(err, errDialed) {
		t.Fatalf("missing host: err = %v", err)
	}
	if *got != "" {
		t.Fatal("dialed after failed lookup")
	}
	if _, err := d.DialContext(context.Background(), "tcp", "no-port"); err == nil {
		t.Fatal("address without port should fail")
	}
}
