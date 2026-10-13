package store_test

import (
	"testing"
	"time"

	"github.com/BOS-Development/esi-proxy/internal/store"
)

func TestListCharacters(t *testing.T) {
	s := newStore(t)
	seedCharacter(t, s, 1, "Alpha")
	seedCharacter(t, s, 2, "Bravo")

	chars, err := s.ListCharacters()
	if err != nil {
		t.Fatalf("ListCharacters: %v", err)
	}
	if len(chars) != 2 || chars[0].Name != "Alpha" || chars[1].Name != "Bravo" {
		t.Fatalf("ListCharacters = %+v, want [Alpha Bravo] ordered by name", chars)
	}
}

func TestFormatAndParseTimeRoundTrip(t *testing.T) {
	now := time.Now()

	formatted := store.FormatTime(now)
	got, err := store.ParseTimeRFC3339(formatted)
	if err != nil {
		t.Fatalf("ParseTimeRFC3339: %v", err)
	}
	if !got.Equal(now) {
		t.Errorf("round trip = %v, want %v", got, now)
	}
}
