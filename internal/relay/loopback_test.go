package relay

import (
	"net"
	"testing"
)

func TestIsLoopback(t *testing.T) {
	cases := []struct {
		addr net.Addr
		want bool
	}{
		{&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5000}, true},
		{&net.UDPAddr{IP: net.ParseIP("127.8.9.10"), Port: 5000}, true},
		{&net.UDPAddr{IP: net.ParseIP("::1"), Port: 5000}, true},
		{&net.UDPAddr{IP: net.ParseIP("203.0.113.5"), Port: 5000}, false},
		{&net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5000}, false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := isLoopback(tc.addr); got != tc.want {
			t.Errorf("isLoopback(%v) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}
