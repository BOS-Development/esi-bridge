// Package tokens manages decrypting, refreshing and re-encrypting character
// access/refresh tokens. It's shared by the proxy's on-demand refresh path
// (server) and the proactive background refresh worker.
package tokens

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/BOS-Development/esi-bridge/internal/cryptox"
	"github.com/BOS-Development/esi-bridge/internal/esi"
	"github.com/BOS-Development/esi-bridge/internal/store"
)

// refreshBuffer is how far ahead of actual expiry a token is proactively
// refreshed, so a request never has to block on a mid-flight refresh.
const refreshBuffer = 2 * time.Minute

// ErrReauthRequired means the character's refresh token can't be used any
// more: EVE SSO rejected it, or it was issued to a different client_id than
// the one now in use. Retrying won't help; the character has to log in
// again. Errors wrapping it carry the human-readable reason.
var ErrReauthRequired = errors.New("character must log in again")

// ClientMismatchReason is the re-authorization reason recorded when a
// character's tokens belong to a different EVE application than the one
// selected in Settings.
const ClientMismatchReason = "This character was added with a different EVE application (Client ID) than the one now selected in Settings. Log it in again, or switch the Client ID back."

type Manager struct {
	store     *store.Store
	sso       *esi.SSOHolder
	masterKey *[32]byte

	// locks holds one *sync.Mutex per character ID. Refreshing and saving a
	// login hold it, so concurrent refreshes of one character (worker, proxy
	// requests, the UI's Refresh button) never race to store token pairs.
	locks sync.Map
}

func NewManager(s *store.Store, sso *esi.SSOHolder, masterKey *[32]byte) *Manager {
	return &Manager{store: s, sso: sso, masterKey: masterKey}
}

func (m *Manager) lock(characterID int64) func() {
	mu, _ := m.locks.LoadOrStore(characterID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	return mu.(*sync.Mutex).Unlock
}

// ReauthReason reports why the character must log in again, or "" if it
// needn't: either the recorded reason, or a mismatch between the client_id
// its tokens were issued to and the one currently in use.
func (m *Manager) ReauthReason(c store.Character) string {
	if c.ReauthReason != "" {
		return c.ReauthReason
	}
	if m.clientMismatch(c) {
		return ClientMismatchReason
	}
	return ""
}

func (m *Manager) clientMismatch(c store.Character) bool {
	return c.ClientID != "" && c.ClientID != m.sso.Get().ClientID()
}

// EnsureAccessToken returns a valid plaintext access token for the character,
// refreshing it first if it's expired or within refreshBuffer of expiring.
// A character flagged for re-authorization is not refreshed; once its access
// token runs out this returns an error wrapping ErrReauthRequired.
func (m *Manager) EnsureAccessToken(ctx context.Context, characterID int64) (string, error) {
	char, err := m.store.GetCharacter(characterID)
	if err != nil {
		return "", fmt.Errorf("get character: %w", err)
	}

	if tokenFresh(char) {
		return m.openAccess(char)
	}
	if char.ReauthReason != "" {
		return "", fmt.Errorf("%w: %s", ErrReauthRequired, char.ReauthReason)
	}
	return m.Refresh(ctx, char)
}

// Refresh exchanges the character's refresh token for a new token pair and
// persists it. char is the caller's snapshot; the current row is re-read
// under the character's lock. If another refresh rewrote the tokens since
// that snapshot and they're still fresh, they're returned as is rather than
// spending the refresh token again.
//
// When EVE SSO rejects the refresh token (invalid_grant), or the token
// belongs to a different client_id, the character is flagged for
// re-authorization and the error wraps ErrReauthRequired.
func (m *Manager) Refresh(ctx context.Context, char store.Character) (string, error) {
	unlock := m.lock(char.ID)
	defer unlock()

	current, err := m.store.GetCharacter(char.ID)
	if err != nil {
		return "", fmt.Errorf("get character: %w", err)
	}
	rewritten := !bytes.Equal(current.RefreshTokenEnc, char.RefreshTokenEnc) ||
		!bytes.Equal(current.AccessTokenEnc, char.AccessTokenEnc)
	if rewritten && tokenFresh(current) {
		return m.openAccess(current)
	}

	sso := m.sso.Get()
	if m.clientMismatch(current) {
		return "", m.flagReauth(current.ID, ClientMismatchReason)
	}

	refreshPlain, err := cryptox.Open(m.masterKey, current.RefreshTokenEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt refresh token: %w", err)
	}

	tok, err := sso.Refresh(ctx, string(refreshPlain))
	if err != nil {
		if reason, ok := invalidGrantReason(err); ok {
			return "", m.flagReauth(current.ID, reason)
		}
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

	if err := m.store.UpdateCharacterTokens(current.ID, refreshEnc, accessEnc, store.FormatTime(tok.Expiry), sso.ClientID()); err != nil {
		return "", fmt.Errorf("persist refreshed tokens: %w", err)
	}

	return tok.AccessToken, nil
}

// flagReauth records that the character must log in again and returns the
// matching ErrReauthRequired error.
func (m *Manager) flagReauth(characterID int64, reason string) error {
	if err := m.store.SetCharacterReauth(characterID, reason); err != nil {
		return fmt.Errorf("%w: %s (and recording that failed: %v)", ErrReauthRequired, reason, err)
	}
	return fmt.Errorf("%w: %s", ErrReauthRequired, reason)
}

// invalidGrantReason reports whether err is EVE SSO's invalid_grant, which
// means the refresh token is dead (revoked, expired, or issued to another
// client) rather than a transient failure, and returns a reason for the UI.
func invalidGrantReason(err error) (string, bool) {
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) || re.ErrorCode != "invalid_grant" {
		return "", false
	}
	reason := "EVE SSO no longer accepts this character's refresh token, usually because its access was revoked on the EVE website or the character changed accounts. Log it in again."
	if d := strings.TrimSpace(re.ErrorDescription); d != "" {
		reason += " (EVE SSO: " + d + ")"
	}
	return reason, true
}

func tokenFresh(c store.Character) bool {
	return time.Now().Add(refreshBuffer).Before(c.AccessTokenExpiresAt)
}

func (m *Manager) openAccess(c store.Character) (string, error) {
	plain, err := cryptox.Open(m.masterKey, c.AccessTokenEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt access token: %w", err)
	}
	return string(plain), nil
}

// SaveLogin encrypts and persists the token pair from a fresh SSO login,
// identifying the character from the access token's JWT claims. It records
// the client_id that issued the tokens and clears any re-authorization flag.
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
		ClientID:             m.sso.Get().ClientID(),
	}

	// Hold the character's lock so a refresh already in flight with the old
	// tokens can't overwrite this login's tokens when it finishes.
	unlock := m.lock(char.ID)
	defer unlock()
	if err := m.store.UpsertCharacter(char); err != nil {
		return store.Character{}, fmt.Errorf("save character: %w", err)
	}
	return char, nil
}
