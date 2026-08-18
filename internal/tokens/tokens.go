// Package tokens manages decrypting, refreshing and re-encrypting character
// access/refresh tokens. It's shared by the proxy's on-demand refresh path
// (server) and the proactive background refresh worker.
package tokens

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/oauth2"

	"github.com/BOS-Development/esi-proxy/internal/cryptox"
	"github.com/BOS-Development/esi-proxy/internal/esi"
	"github.com/BOS-Development/esi-proxy/internal/store"
)

// refreshBuffer is how far ahead of actual expiry a token is proactively
// refreshed, so a request never has to block on a mid-flight refresh.
const refreshBuffer = 2 * time.Minute

type Manager struct {
	store     *store.Store
	sso       *esi.SSOHolder
	masterKey *[32]byte
}

func NewManager(s *store.Store, sso *esi.SSOHolder, masterKey *[32]byte) *Manager {
	return &Manager{store: s, sso: sso, masterKey: masterKey}
}

// EnsureAccessToken returns a valid plaintext access token for the character,
// refreshing it first if it's expired or within refreshBuffer of expiring.
func (m *Manager) EnsureAccessToken(ctx context.Context, characterID int64) (string, error) {
	char, err := m.store.GetCharacter(characterID)
	if err != nil {
		return "", fmt.Errorf("get character: %w", err)
	}

	if time.Now().Add(refreshBuffer).Before(char.AccessTokenExpiresAt) {
		plain, err := cryptox.Open(m.masterKey, char.AccessTokenEnc)
		if err != nil {
			return "", fmt.Errorf("decrypt access token: %w", err)
		}
		return string(plain), nil
	}

	return m.Refresh(ctx, char)
}

// Refresh unconditionally exchanges the character's refresh token for a new
// token pair and persists it.
func (m *Manager) Refresh(ctx context.Context, char store.Character) (string, error) {
	refreshPlain, err := cryptox.Open(m.masterKey, char.RefreshTokenEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt refresh token: %w", err)
	}

	tok, err := m.sso.Get().Refresh(ctx, string(refreshPlain))
	if err != nil {
		return "", fmt.Errorf("refresh with EVE SSO: %w", err)
	}

	newRefresh := tok.RefreshToken
	if newRefresh == "" {
		newRefresh = string(refreshPlain) // EVE SSO usually keeps the refresh token stable
	}

	refreshEnc, err := cryptox.Seal(m.masterKey, []byte(newRefresh))
	if err != nil {
		return "", fmt.Errorf("encrypt refresh token: %w", err)
	}
	accessEnc, err := cryptox.Seal(m.masterKey, []byte(tok.AccessToken))
	if err != nil {
		return "", fmt.Errorf("encrypt access token: %w", err)
	}

	if err := m.store.UpdateCharacterTokens(char.ID, refreshEnc, accessEnc, store.FormatTime(tok.Expiry)); err != nil {
		return "", fmt.Errorf("persist refreshed tokens: %w", err)
	}

	return tok.AccessToken, nil
}

// SaveLogin encrypts and persists the token pair from a fresh SSO login,
// identifying the character from the access token's JWT claims.
func (m *Manager) SaveLogin(accountID int64, tok *oauth2.Token) (store.Character, error) {
	claims, err := esi.ParseCharacterClaims(tok.AccessToken)
	if err != nil {
		return store.Character{}, fmt.Errorf("parse character claims: %w", err)
	}

	refreshEnc, err := cryptox.Seal(m.masterKey, []byte(tok.RefreshToken))
	if err != nil {
		return store.Character{}, fmt.Errorf("encrypt refresh token: %w", err)
	}
	accessEnc, err := cryptox.Seal(m.masterKey, []byte(tok.AccessToken))
	if err != nil {
		return store.Character{}, fmt.Errorf("encrypt access token: %w", err)
	}

	char := store.Character{
		ID:                   claims.CharacterID,
		AccountID:            accountID,
		Name:                 claims.CharacterName,
		RefreshTokenEnc:      refreshEnc,
		AccessTokenEnc:       accessEnc,
		AccessTokenExpiresAt: tok.Expiry,
		Scopes:               claims.Scopes,
	}
	if err := m.store.UpsertCharacter(char); err != nil {
		return store.Character{}, fmt.Errorf("save character: %w", err)
	}
	return char, nil
}
