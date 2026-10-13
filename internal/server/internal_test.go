package server

import (
	"encoding/hex"
	"testing"
)

func TestCoversScopes(t *testing.T) {
	tests := []struct {
		name      string
		granted   []string
		requested []string
		want      bool
	}{
		{"empty request is always covered", []string{"a"}, nil, true},
		{"exact subset", []string{"a", "b", "c"}, []string{"a", "c"}, true},
		{"identical", []string{"a", "b"}, []string{"a", "b"}, true},
		{"missing one scope", []string{"a", "b"}, []string{"a", "z"}, false},
		{"nothing granted", nil, []string{"a"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := coversScopes(tt.granted, tt.requested); got != tt.want {
				t.Errorf("coversScopes(%v, %v) = %v, want %v", tt.granted, tt.requested, got, tt.want)
			}
		})
	}
}

func TestRandomStateIsHexAndUnique(t *testing.T) {
	a, err := randomState()
	if err != nil {
		t.Fatalf("randomState: %v", err)
	}
	if len(a) != 32 { // 16 random bytes, hex-encoded
		t.Errorf("state length = %d, want 32 hex chars", len(a))
	}
	if _, err := hex.DecodeString(a); err != nil {
		t.Errorf("state is not valid hex: %v", err)
	}
	b, _ := randomState()
	if a == b {
		t.Error("two randomState calls returned the same value")
	}
}

func TestHashAPIKeyDeterministic(t *testing.T) {
	h1 := hashAPIKey("key")
	h2 := hashAPIKey("key")
	if h1 != h2 {
		t.Error("hashAPIKey is not deterministic")
	}
	if len(h1) != 64 { // sha256 hex
		t.Errorf("hash length = %d, want 64", len(h1))
	}
	if hashAPIKey("other") == h1 {
		t.Error("different keys produced the same hash")
	}
}

func TestGenerateAPIKeyUnique(t *testing.T) {
	k1, err := generateAPIKey()
	if err != nil {
		t.Fatalf("generateAPIKey: %v", err)
	}
	if len(k1) != 64 { // 32 random bytes, hex-encoded
		t.Errorf("key length = %d, want 64", len(k1))
	}
	k2, _ := generateAPIKey()
	if k1 == k2 {
		t.Error("two generateAPIKey calls returned the same value")
	}
}
