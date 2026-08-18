package store

import (
	"database/sql"
	"errors"
)

// RequestAppPermission records that an application wants an app-wide
// permission (see PermissionListCharacters/PermissionListAccounts),
// creating or refreshing a pending_app_permissions row.
func (s *Store) RequestAppPermission(applicationID int64, permission string) error {
	_, err := s.db.Exec(`
		INSERT INTO pending_app_permissions (application_id, permission, requested_at)
		VALUES (?, ?, ?)
		ON CONFLICT(application_id, permission) DO UPDATE SET requested_at = excluded.requested_at
	`, applicationID, permission, now())
	return err
}

// HasAppPermission reports whether the application currently holds an
// active (non-revoked) app-wide permission.
func (s *Store) HasAppPermission(applicationID int64, permission string) (bool, error) {
	var exists int
	err := s.db.QueryRow(`
		SELECT 1 FROM app_permissions WHERE application_id = ? AND permission = ? AND revoked_at IS NULL
	`, applicationID, permission).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) ListPendingAppPermissions() ([]PendingAppPermission, error) {
	rows, err := s.db.Query(`
		SELECT p.id, p.application_id, a.name, p.permission, p.requested_at
		FROM pending_app_permissions p
		JOIN applications a ON a.id = p.application_id
		ORDER BY p.requested_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PendingAppPermission
	for rows.Next() {
		var p PendingAppPermission
		var requestedAt string
		if err := rows.Scan(&p.ID, &p.ApplicationID, &p.ApplicationName, &p.Permission, &requestedAt); err != nil {
			return nil, err
		}
		p.RequestedAt = parseTime(requestedAt)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ApprovePendingAppPermission turns a pending request into an active
// app-wide permission.
func (s *Store) ApprovePendingAppPermission(pendingID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var appID int64
	var permission string
	err = tx.QueryRow(`SELECT application_id, permission FROM pending_app_permissions WHERE id = ?`, pendingID).
		Scan(&appID, &permission)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO app_permissions (application_id, permission, approved_at)
		VALUES (?, ?, ?)
		ON CONFLICT(application_id, permission) DO UPDATE SET approved_at = excluded.approved_at, revoked_at = NULL
	`, appID, permission, now())
	if err != nil {
		return err
	}

	if _, err := tx.Exec(`DELETE FROM pending_app_permissions WHERE id = ?`, pendingID); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) DenyPendingAppPermission(pendingID int64) error {
	res, err := s.db.Exec(`DELETE FROM pending_app_permissions WHERE id = ?`, pendingID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListActiveAppPermissionsView() ([]AppPermissionView, error) {
	rows, err := s.db.Query(`
		SELECT p.id, p.application_id, a.name, p.permission, p.approved_at
		FROM app_permissions p
		JOIN applications a ON a.id = p.application_id
		WHERE p.revoked_at IS NULL
		ORDER BY a.name, p.permission
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AppPermissionView
	for rows.Next() {
		var v AppPermissionView
		var approvedAt string
		if err := rows.Scan(&v.ID, &v.ApplicationID, &v.ApplicationName, &v.Permission, &approvedAt); err != nil {
			return nil, err
		}
		v.ApprovedAt = parseTime(approvedAt)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) RevokeAppPermission(id int64) error {
	res, err := s.db.Exec(`UPDATE app_permissions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
