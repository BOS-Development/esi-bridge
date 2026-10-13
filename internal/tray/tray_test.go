package tray

import "testing"

// The LAN address shown in the tray now comes from the live Network
// (connectTarget and discovery.LANIP, tested in cmd/esi-proxy and
// internal/discovery), so only the checkbox's bind classification lives here.
func TestIsLANBind(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", false},
		{"127.0.0.1", false},
		{"::1", false},
		{"[::1]", false},
		{"", false},
		{"0.0.0.0", true},
		{"::", true},
		{"[::]", true},
		{"192.168.1.50", true},
		{"10.0.0.1", true},
	}
	for _, tt := range tests {
		if got := isLANBind(tt.host); got != tt.want {
			t.Errorf("isLANBind(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}
