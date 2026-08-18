package esi

import (
	"encoding/base64"
	"testing"
)

func fakeJWT(t *testing.T, payloadJSON string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(payloadJSON))
	return header + "." + payload + ".signature-not-checked"
}

func TestParseCharacterClaimsMultipleScopes(t *testing.T) {
	tok := fakeJWT(t, `{"sub":"CHARACTER:EVE:2112625428","name":"CCP Bartender","scp":["esi-assets.read_assets.v1","esi-skills.read_skills.v1"]}`)

	claims, err := ParseCharacterClaims(tok)
	if err != nil {
		t.Fatalf("ParseCharacterClaims: %v", err)
	}
	if claims.CharacterID != 2112625428 {
		t.Errorf("CharacterID = %d, want 2112625428", claims.CharacterID)
	}
	if claims.CharacterName != "CCP Bartender" {
		t.Errorf("CharacterName = %q", claims.CharacterName)
	}
	if len(claims.Scopes) != 2 {
		t.Errorf("Scopes = %v, want 2 entries", claims.Scopes)
	}
}

func TestParseCharacterClaimsSingleScope(t *testing.T) {
	tok := fakeJWT(t, `{"sub":"CHARACTER:EVE:42","name":"Solo Scope","scp":"esi-assets.read_assets.v1"}`)

	claims, err := ParseCharacterClaims(tok)
	if err != nil {
		t.Fatalf("ParseCharacterClaims: %v", err)
	}
	if len(claims.Scopes) != 1 || claims.Scopes[0] != "esi-assets.read_assets.v1" {
		t.Errorf("Scopes = %v", claims.Scopes)
	}
}

func TestParseCharacterClaimsRejectsMalformed(t *testing.T) {
	if _, err := ParseCharacterClaims("not-a-jwt"); err == nil {
		t.Fatal("expected error for a non-JWT string")
	}

	tok := fakeJWT(t, `{"sub":"USER:42","name":"Nope"}`)
	if _, err := ParseCharacterClaims(tok); err == nil {
		t.Fatal("expected error for a non-CHARACTER sub claim")
	}
}
