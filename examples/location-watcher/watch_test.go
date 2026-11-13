package main

import (
	"testing"
	"time"
)

func TestUntilRefresh(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		target time.Time
		want   time.Duration
	}{
		{"future beyond floor", now.Add(30 * time.Second), 30 * time.Second},
		{"already expired", now.Add(-time.Minute), fallbackInterval},
		{"inside the floor", now.Add(time.Second), fallbackInterval},
		{"exactly now", now, fallbackInterval},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := untilRefresh(now, tt.target); got != tt.want {
				t.Fatalf("untilRefresh(%v) = %v, want %v", tt.target, got, tt.want)
			}
		})
	}
}

func TestEarliest(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 5, 0, time.UTC)
	ship := base.Add(-2 * time.Second)     // sooner than base
	location := base.Add(10 * time.Second) // later than base

	// The soonest non-zero time wins; the zero time is ignored.
	if got := earliest(base, ship, location); !got.Equal(ship) {
		t.Fatalf("earliest = %v, want %v (the ship expiry)", got, ship)
	}
	if got := earliest(base, time.Time{}, location); !got.Equal(base) {
		t.Fatalf("earliest with a later-only and zero time = %v, want base %v", got, base)
	}
}

func TestParseExpires(t *testing.T) {
	if got := parseExpires(""); !got.IsZero() {
		t.Fatalf("parseExpires(\"\") = %v, want zero time", got)
	}
	if got := parseExpires("not a date"); !got.IsZero() {
		t.Fatalf("parseExpires(garbage) = %v, want zero time", got)
	}
	want := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	if got := parseExpires("Fri, 02 Jan 2026 15:04:05 GMT"); !got.Equal(want) {
		t.Fatalf("parseExpires(valid) = %v, want %v", got, want)
	}
}
