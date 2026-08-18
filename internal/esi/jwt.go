package esi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// CharacterClaims is the subset of an EVE SSO JWT access token's claims the
// proxy needs to identify who logged in.
type CharacterClaims struct {
	CharacterID   int64
	CharacterName string
	Scopes        []string
}

// ParseCharacterClaims extracts character identity from a JWT access token
// issued by EVE SSO. It decodes the payload structurally without verifying
// the signature: the token was obtained directly from login.eveonline.com's
// token endpoint over TLS in the same exchange, so an untrusted third party
// could not have forged it in transit.
func ParseCharacterClaims(accessToken string) (CharacterClaims, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return CharacterClaims{}, fmt.Errorf("access token is not a JWT")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return CharacterClaims{}, fmt.Errorf("decode jwt payload: %w", err)
	}

	var claims struct {
		Sub   string `json:"sub"` // "CHARACTER:EVE:<id>"
		Name  string `json:"name"`
		Scope any    `json:"scp"` // string or []string depending on scope count
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return CharacterClaims{}, fmt.Errorf("unmarshal jwt claims: %w", err)
	}

	subParts := strings.Split(claims.Sub, ":")
	if len(subParts) != 3 || subParts[0] != "CHARACTER" {
		return CharacterClaims{}, fmt.Errorf("unexpected sub claim %q", claims.Sub)
	}
	charID, err := strconv.ParseInt(subParts[2], 10, 64)
	if err != nil {
		return CharacterClaims{}, fmt.Errorf("parse character id: %w", err)
	}

	var scopes []string
	switch v := claims.Scope.(type) {
	case string:
		if v != "" {
			scopes = []string{v}
		}
	case []any:
		for _, s := range v {
			if str, ok := s.(string); ok {
				scopes = append(scopes, str)
			}
		}
	}

	return CharacterClaims{CharacterID: charID, CharacterName: claims.Name, Scopes: scopes}, nil
}
