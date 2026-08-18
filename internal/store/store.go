// Package store provides repository access to the proxy's SQLite tables:
// accounts, characters, applications, grants and pending_grants.
package store

import (
	"database/sql"
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store { return &Store{db: db} }

// --- accounts ---

func (s *Store) CreateAccount(name string) (Account, error) {
	res, err := s.db.Exec(`INSERT INTO accounts (name, created_at) VALUES (?, ?)`, name, now())
	if err != nil {
		return Account{}, fmt.Errorf("create account: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetAccount(id)
}

func (s *Store) GetAccount(id int64) (Account, error) {
	var a Account
	var createdAt string
	err := s.db.QueryRow(`SELECT id, name, created_at FROM accounts WHERE id = ?`, id).
		Scan(&a.ID, &a.Name, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, err
	}
	a.CreatedAt = parseTime(createdAt)
	return a, nil
}

// DefaultAccountName is the account newly-logged-in characters are grouped
// under when no other account management UI is in play (v1's tray doesn't
// expose creating/renaming accounts; the schema supports it for later).
const DefaultAccountName = "Main"

// GetOrCreateDefaultAccount returns the "Main" account, creating it if this
// is the first character ever added.
func (s *Store) GetOrCreateDefaultAccount() (Account, error) {
	return s.GetOrCreateAccountByName(DefaultAccountName)
}

// GetOrCreateAccountByName looks up an account by name, creating it if
// absent. Used by default-account lookup and by bundle import, which
// recreates accounts by the name they had on the exporting machine.
func (s *Store) GetOrCreateAccountByName(name string) (Account, error) {
	var a Account
	var createdAt string
	err := s.db.QueryRow(`SELECT id, name, created_at FROM accounts WHERE name = ?`, name).
		Scan(&a.ID, &a.Name, &createdAt)
	if err == nil {
		a.CreatedAt = parseTime(createdAt)
		return a, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Account{}, err
	}
	return s.CreateAccount(name)
}

func (s *Store) ListAccounts() ([]Account, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at FROM accounts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		var a Account
		var createdAt string
		if err := rows.Scan(&a.ID, &a.Name, &createdAt); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTime(createdAt)
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- characters ---

// UpsertCharacter stores or replaces a character's tokens, e.g. after SSO
// login or an import.
func (s *Store) UpsertCharacter(c Character) error {
	_, err := s.db.Exec(`
		INSERT INTO characters (id, account_id, name, refresh_token_enc, access_token_enc, access_token_expires_at, scopes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			account_id = excluded.account_id,
			name = excluded.name,
			refresh_token_enc = excluded.refresh_token_enc,
			access_token_enc = excluded.access_token_enc,
			access_token_expires_at = excluded.access_token_expires_at,
			scopes = excluded.scopes
	`, c.ID, c.AccountID, c.Name, c.RefreshTokenEnc, c.AccessTokenEnc,
		FormatTime(c.AccessTokenExpiresAt), joinScopes(c.Scopes), now())
	if err != nil {
		return fmt.Errorf("upsert character: %w", err)
	}
	return nil
}

// UpdateCharacterTokens is called after a token refresh, which may rotate
// the refresh token as well as issuing a new access token.
func (s *Store) UpdateCharacterTokens(id int64, refreshTokenEnc, accessTokenEnc []byte, expiresAt string) error {
	res, err := s.db.Exec(`UPDATE characters SET refresh_token_enc = ?, access_token_enc = ?, access_token_expires_at = ? WHERE id = ?`,
		refreshTokenEnc, accessTokenEnc, expiresAt, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanCharacter(row interface{ Scan(...any) error }) (Character, error) {
	var c Character
	var expiresAt, createdAt, scopes string
	err := row.Scan(&c.ID, &c.AccountID, &c.Name, &c.RefreshTokenEnc, &c.AccessTokenEnc, &expiresAt, &scopes, &createdAt)
	if err != nil {
		return Character{}, err
	}
	c.AccessTokenExpiresAt = parseTime(expiresAt)
	c.CreatedAt = parseTime(createdAt)
	c.Scopes = splitScopes(scopes)
	return c, nil
}

const characterColumns = `id, account_id, name, refresh_token_enc, access_token_enc, access_token_expires_at, scopes, created_at`

func (s *Store) GetCharacter(id int64) (Character, error) {
	row := s.db.QueryRow(`SELECT `+characterColumns+` FROM characters WHERE id = ?`, id)
	c, err := scanCharacter(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Character{}, ErrNotFound
	}
	return c, err
}

func (s *Store) ListCharacters() ([]Character, error) {
	rows, err := s.db.Query(`SELECT ` + characterColumns + ` FROM characters ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Character
	for rows.Next() {
		c, err := scanCharacter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListCharactersExpiringBefore returns characters whose access token expires
// before the given RFC3339Nano timestamp, for the refresh worker.
func (s *Store) ListCharactersExpiringBefore(cutoffRFC3339 string) ([]Character, error) {
	rows, err := s.db.Query(`SELECT `+characterColumns+` FROM characters WHERE access_token_expires_at < ?`, cutoffRFC3339)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Character
	for rows.Next() {
		c, err := scanCharacter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- applications ---

func (s *Store) CreateApplication(name, apiKeyHash string) (Application, error) {
	res, err := s.db.Exec(`INSERT INTO applications (name, api_key_hash, created_at) VALUES (?, ?, ?)`,
		name, apiKeyHash, now())
	if err != nil {
		return Application{}, fmt.Errorf("create application: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetApplication(id)
}

func (s *Store) GetApplication(id int64) (Application, error) {
	var a Application
	var createdAt string
	err := s.db.QueryRow(`SELECT id, name, api_key_hash, created_at FROM applications WHERE id = ?`, id).
		Scan(&a.ID, &a.Name, &a.APIKeyHash, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Application{}, ErrNotFound
	}
	if err != nil {
		return Application{}, err
	}
	a.CreatedAt = parseTime(createdAt)
	return a, nil
}

func (s *Store) GetApplicationByKeyHash(hash string) (Application, error) {
	var a Application
	var createdAt string
	err := s.db.QueryRow(`SELECT id, name, api_key_hash, created_at FROM applications WHERE api_key_hash = ?`, hash).
		Scan(&a.ID, &a.Name, &a.APIKeyHash, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Application{}, ErrNotFound
	}
	if err != nil {
		return Application{}, err
	}
	a.CreatedAt = parseTime(createdAt)
	return a, nil
}

// --- grants & pending grants ---

// UpsertPendingGrant records that an application wants the given scopes for
// a character, creating or refreshing a pending_grants row.
func (s *Store) UpsertPendingGrant(applicationID, characterID int64, scopes []string) error {
	_, err := s.db.Exec(`
		INSERT INTO pending_grants (application_id, character_id, requested_scopes, requested_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(application_id, character_id) DO UPDATE SET
			requested_scopes = excluded.requested_scopes,
			requested_at = excluded.requested_at
	`, applicationID, characterID, joinScopes(scopes), now())
	return err
}

func (s *Store) ListPendingGrants() ([]PendingGrant, error) {
	rows, err := s.db.Query(`
		SELECT pg.id, pg.application_id, a.name, pg.character_id, c.name, pg.requested_scopes, pg.requested_at
		FROM pending_grants pg
		JOIN applications a ON a.id = pg.application_id
		JOIN characters c ON c.id = pg.character_id
		ORDER BY pg.requested_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PendingGrant
	for rows.Next() {
		var p PendingGrant
		var scopes, requestedAt string
		if err := rows.Scan(&p.ID, &p.ApplicationID, &p.ApplicationName, &p.CharacterID, &p.CharacterName, &scopes, &requestedAt); err != nil {
			return nil, err
		}
		p.RequestedScopes = splitScopes(scopes)
		p.RequestedAt = parseTime(requestedAt)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ApprovePendingGrant turns a pending request into an active grant.
func (s *Store) ApprovePendingGrant(pendingID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var appID, charID int64
	var scopes string
	err = tx.QueryRow(`SELECT application_id, character_id, requested_scopes FROM pending_grants WHERE id = ?`, pendingID).
		Scan(&appID, &charID, &scopes)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO grants (application_id, character_id, scopes, approved_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(application_id, character_id) DO UPDATE SET
			scopes = excluded.scopes, approved_at = excluded.approved_at, revoked_at = NULL
	`, appID, charID, scopes, now())
	if err != nil {
		return err
	}

	if _, err := tx.Exec(`DELETE FROM pending_grants WHERE id = ?`, pendingID); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) DenyPendingGrant(pendingID int64) error {
	res, err := s.db.Exec(`DELETE FROM pending_grants WHERE id = ?`, pendingID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetActiveGrant returns the non-revoked grant for an application/character
// pair, if any.
func (s *Store) GetActiveGrant(applicationID, characterID int64) (Grant, error) {
	var g Grant
	var scopes, approvedAt string
	var revokedAt sql.NullString
	err := s.db.QueryRow(`
		SELECT id, application_id, character_id, scopes, approved_at, revoked_at
		FROM grants WHERE application_id = ? AND character_id = ? AND revoked_at IS NULL
	`, applicationID, characterID).Scan(&g.ID, &g.ApplicationID, &g.CharacterID, &scopes, &approvedAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, ErrNotFound
	}
	if err != nil {
		return Grant{}, err
	}
	g.Scopes = splitScopes(scopes)
	g.ApprovedAt = parseTime(approvedAt)
	return g, nil
}

func (s *Store) ListActiveGrants() ([]Grant, error) {
	rows, err := s.db.Query(`SELECT id, application_id, character_id, scopes, approved_at, revoked_at FROM grants WHERE revoked_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Grant
	for rows.Next() {
		var g Grant
		var scopes, approvedAt string
		var revokedAt sql.NullString
		if err := rows.Scan(&g.ID, &g.ApplicationID, &g.CharacterID, &scopes, &approvedAt, &revokedAt); err != nil {
			return nil, err
		}
		g.Scopes = splitScopes(scopes)
		g.ApprovedAt = parseTime(approvedAt)
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListActiveGrantsView returns active grants joined with application and
// character names, for display (e.g. in the tray menu).
func (s *Store) ListActiveGrantsView() ([]GrantView, error) {
	rows, err := s.db.Query(`
		SELECT g.id, g.application_id, a.name, g.character_id, c.name, g.scopes, g.approved_at
		FROM grants g
		JOIN applications a ON a.id = g.application_id
		JOIN characters c ON c.id = g.character_id
		WHERE g.revoked_at IS NULL
		ORDER BY a.name, c.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GrantView
	for rows.Next() {
		var g GrantView
		var scopes, approvedAt string
		if err := rows.Scan(&g.ID, &g.ApplicationID, &g.ApplicationName, &g.CharacterID, &g.CharacterName, &scopes, &approvedAt); err != nil {
			return nil, err
		}
		g.Scopes = splitScopes(scopes)
		g.ApprovedAt = parseTime(approvedAt)
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListActiveGrantsForApplication returns the given application's own active
// grants, for the /api/grants endpoint — an app can check what it currently
// has access to without probing every proxy path.
func (s *Store) ListActiveGrantsForApplication(applicationID int64) ([]GrantView, error) {
	rows, err := s.db.Query(`
		SELECT g.id, g.application_id, a.name, g.character_id, c.name, g.scopes, g.approved_at
		FROM grants g
		JOIN applications a ON a.id = g.application_id
		JOIN characters c ON c.id = g.character_id
		WHERE g.revoked_at IS NULL AND g.application_id = ?
		ORDER BY c.name
	`, applicationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GrantView
	for rows.Next() {
		var g GrantView
		var scopes, approvedAt string
		if err := rows.Scan(&g.ID, &g.ApplicationID, &g.ApplicationName, &g.CharacterID, &g.CharacterName, &scopes, &approvedAt); err != nil {
			return nil, err
		}
		g.Scopes = splitScopes(scopes)
		g.ApprovedAt = parseTime(approvedAt)
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) RevokeGrant(id int64) error {
	res, err := s.db.Exec(`UPDATE grants SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
