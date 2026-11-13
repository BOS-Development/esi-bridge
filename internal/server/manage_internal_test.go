package server

import "testing"

func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		remoteAddr string
		want       bool
	}{
		{"127.0.0.1:54321", true},
		{"[::1]:8085", true},
		{"192.168.1.20:40000", false},
		{"10.0.0.5:1234", false},
		{"8.8.8.8:80", false},
		{"127.0.0.1", true},  // no port
		{"not-an-ip:80", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isLoopbackAddr(c.remoteAddr); got != c.want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v", c.remoteAddr, got, c.want)
		}
	}
}
