package store

import (
	"strings"
	"time"
)

type Account struct {
	ID        int64
	Name      string
	CreatedAt time.Time
}

type Character struct {
	ID                   int64
	AccountID            int64
	Name                 string
	RefreshTokenEnc      []byte
	AccessTokenEnc       []byte
	AccessTokenExpiresAt time.Time
	Scopes               []string
	CreatedAt            time.Time
	// TokensUpdatedAt is when the token pair was last written (login or
	// refresh), used to show the age of the character's tokens.
	TokensUpdatedAt time.Time
}

type Application struct {
	ID         int64
	Name       string
	APIKeyHash string
	CreatedAt  time.Time
}

type Grant struct {
	ID            int64
	ApplicationID int64
	CharacterID   int64
	Scopes        []string
	ApprovedAt    time.Time
	RevokedAt     *time.Time
}

// GrantView is an active grant joined with the application and character
// names for display.
type GrantView struct {
	ID              int64
	ApplicationID   int64
	ApplicationName string
	CharacterID     int64
	CharacterName   string
	Scopes          []string
	ApprovedAt      time.Time
}

// PendingGrant is a grant request awaiting user approval, joined with the
// application and character names for display (e.g. in the tray menu).
type PendingGrant struct {
	ID              int64
	ApplicationID   int64
	ApplicationName string
	CharacterID     int64
	CharacterName   string
	RequestedScopes []string
	RequestedAt     time.Time
}

// App-wide permissions: unlike Grant/PendingGrant, these aren't tied to a
// single character. PermissionListCharacters unlocks GET /api/characters
// returning every character on the proxy (id/name only) instead of just the
// ones the app already has a per-character grant for. PermissionListAccounts
// unlocks GET /api/accounts and account_id on character listings.
const (
	PermissionListCharacters = "list_characters"
	PermissionListAccounts   = "list_accounts"
)

// KnownAppPermissions is the allowlist /api/request-permissions validates
// against, so an app can't invent arbitrary permission strings.
var KnownAppPermissions = map[string]bool{
	PermissionListCharacters: true,
	PermissionListAccounts:   true,
}

// AppPermissionView is an active app-wide permission joined with the
// application name for display.
type AppPermissionView struct {
	ID              int64
	ApplicationID   int64
	ApplicationName string
	Permission      string
	ApprovedAt      time.Time
}

// PendingAppPermission is an app-wide permission request awaiting approval.
type PendingAppPermission struct {
	ID              int64
	ApplicationID   int64
	ApplicationName string
	Permission      string
	RequestedAt     time.Time
}

func joinScopes(scopes []string) string { return strings.Join(scopes, " ") }

func splitScopes(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func now() string { return FormatTime(time.Now()) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// FormatTime renders t in the RFC3339Nano form used for all timestamp
// columns, so callers outside this package (esi, worker) can produce values
// compatible with UpdateCharacterAccessToken and ListCharactersExpiringBefore.
func FormatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ParseTimeRFC3339 parses a timestamp in the form produced by FormatTime,
// for callers outside this package (e.g. bundle import) reconstructing a
// Character from serialized data.
func ParseTimeRFC3339(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }
