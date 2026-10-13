package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/BOS-Development/esi-proxy/internal/db"
	"github.com/BOS-Development/esi-proxy/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	conn, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return store.New(conn)
}

// seedCharacter creates an account and a character under it, returning both.
func seedCharacter(t *testing.T, s *store.Store, id int64, name string) (store.Account, store.Character) {
	t.Helper()
	acc, err := s.GetOrCreateDefaultAccount()
	if err != nil {
		t.Fatalf("GetOrCreateDefaultAccount: %v", err)
	}
	char := store.Character{
		ID:                   id,
		AccountID:            acc.ID,
		Name:                 name,
		RefreshTokenEnc:      []byte("refresh-enc"),
		AccessTokenEnc:       []byte("access-enc"),
		AccessTokenExpiresAt: time.Now().Add(time.Hour),
		Scopes:               []string{"esi-assets.read_assets.v1"},
	}
	if err := s.UpsertCharacter(char); err != nil {
		t.Fatalf("UpsertCharacter: %v", err)
	}
	return acc, char
}

func seedApp(t *testing.T, s *store.Store, name string) store.Application {
	t.Helper()
	app, err := s.CreateApplication(name, name+"-hash")
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	return app
}

// --- accounts ---

func TestAccountsCreateGetList(t *testing.T) {
	s := newStore(t)

	a, err := s.CreateAccount("Alpha")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if a.ID == 0 || a.Name != "Alpha" {
		t.Fatalf("CreateAccount returned %+v", a)
	}

	got, err := s.GetAccount(a.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.Name != "Alpha" {
		t.Errorf("GetAccount name = %q", got.Name)
	}

	if _, err := s.CreateAccount("Beta"); err != nil {
		t.Fatalf("CreateAccount Beta: %v", err)
	}
	accounts, err := s.ListAccounts()
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 2 || accounts[0].Name != "Alpha" || accounts[1].Name != "Beta" {
		t.Fatalf("ListAccounts = %+v, want [Alpha Beta] ordered by name", accounts)
	}
}

func TestGetAccountNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.GetAccount(404); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetAccount(missing) err = %v, want ErrNotFound", err)
	}
}

func TestGetOrCreateAccountByNameReuses(t *testing.T) {
	s := newStore(t)
	a1, err := s.GetOrCreateAccountByName("Main")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	a2, err := s.GetOrCreateAccountByName("Main")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a1.ID != a2.ID {
		t.Fatalf("GetOrCreateAccountByName created a duplicate: %d != %d", a1.ID, a2.ID)
	}
}

// --- characters ---

func TestCharacterUpsertGetUpdate(t *testing.T) {
	s := newStore(t)
	_, char := seedCharacter(t, s, 100, "Pilot One")

	got, err := s.GetCharacter(100)
	if err != nil {
		t.Fatalf("GetCharacter: %v", err)
	}
	if got.Name != "Pilot One" || len(got.Scopes) != 1 {
		t.Fatalf("GetCharacter = %+v", got)
	}

	// Upsert again with a changed name: ON CONFLICT should update in place.
	char.Name = "Pilot Renamed"
	if err := s.UpsertCharacter(char); err != nil {
		t.Fatalf("re-UpsertCharacter: %v", err)
	}
	got, _ = s.GetCharacter(100)
	if got.Name != "Pilot Renamed" {
		t.Errorf("after upsert, name = %q, want Pilot Renamed", got.Name)
	}

	// UpdateCharacterTokens rotates the stored tokens.
	newExpiry := store.FormatTime(time.Now().Add(2 * time.Hour))
	if err := s.UpdateCharacterTokens(100, []byte("r2"), []byte("a2"), newExpiry); err != nil {
		t.Fatalf("UpdateCharacterTokens: %v", err)
	}
	got, _ = s.GetCharacter(100)
	if string(got.RefreshTokenEnc) != "r2" || string(got.AccessTokenEnc) != "a2" {
		t.Errorf("tokens not updated: %+v", got)
	}
}

func TestGetCharacterNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.GetCharacter(1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetCharacter(missing) err = %v, want ErrNotFound", err)
	}
}

func TestUpdateCharacterTokensNotFound(t *testing.T) {
	s := newStore(t)
	err := s.UpdateCharacterTokens(1, []byte("r"), []byte("a"), store.FormatTime(time.Now()))
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateCharacterTokens(missing) err = %v, want ErrNotFound", err)
	}
}

func TestListCharactersExpiringBefore(t *testing.T) {
	s := newStore(t)
	acc, _ := seedCharacter(t, s, 1, "Fresh")

	// Overwrite "Fresh" to expire well in the future, add "Stale" expired.
	if err := s.UpsertCharacter(store.Character{
		ID: 1, AccountID: acc.ID, Name: "Fresh",
		RefreshTokenEnc: []byte("r"), AccessTokenEnc: []byte("a"),
		AccessTokenExpiresAt: time.Now().Add(10 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertCharacter(store.Character{
		ID: 2, AccountID: acc.ID, Name: "Stale",
		RefreshTokenEnc: []byte("r"), AccessTokenEnc: []byte("a"),
		AccessTokenExpiresAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	cutoff := store.FormatTime(time.Now().Add(5 * time.Minute))
	due, err := s.ListCharactersExpiringBefore(cutoff)
	if err != nil {
		t.Fatalf("ListCharactersExpiringBefore: %v", err)
	}
	if len(due) != 1 || due[0].ID != 2 {
		t.Fatalf("expiring = %+v, want only the stale character (id 2)", due)
	}
}

// --- applications ---

func TestApplicationCreateAndLookup(t *testing.T) {
	s := newStore(t)
	app, err := s.CreateApplication("Pyfa", "hash-123")
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}

	byID, err := s.GetApplication(app.ID)
	if err != nil || byID.Name != "Pyfa" {
		t.Fatalf("GetApplication = %+v, err %v", byID, err)
	}
	byHash, err := s.GetApplicationByKeyHash("hash-123")
	if err != nil || byHash.ID != app.ID {
		t.Fatalf("GetApplicationByKeyHash = %+v, err %v", byHash, err)
	}

	if _, err := s.GetApplicationByKeyHash("nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetApplicationByKeyHash(missing) err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetApplication(999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetApplication(missing) err = %v, want ErrNotFound", err)
	}
}

// --- grants ---

func TestGrantLifecycle(t *testing.T) {
	s := newStore(t)
	seedCharacter(t, s, 100, "Pilot")
	app := seedApp(t, s, "Pyfa")

	// Request → pending.
	if err := s.UpsertPendingGrant(app.ID, 100, []string{"esi-assets.read_assets.v1"}); err != nil {
		t.Fatalf("UpsertPendingGrant: %v", err)
	}
	pending, err := s.ListPendingGrants()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPendingGrants = %+v, err %v", pending, err)
	}
	if pending[0].ApplicationName != "Pyfa" || pending[0].CharacterName != "Pilot" {
		t.Errorf("pending view not joined: %+v", pending[0])
	}

	// No active grant yet.
	if _, err := s.GetActiveGrant(app.ID, 100); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetActiveGrant before approval err = %v, want ErrNotFound", err)
	}

	// Approve → active grant, pending cleared.
	if err := s.ApprovePendingGrant(pending[0].ID); err != nil {
		t.Fatalf("ApprovePendingGrant: %v", err)
	}
	grant, err := s.GetActiveGrant(app.ID, 100)
	if err != nil {
		t.Fatalf("GetActiveGrant after approval: %v", err)
	}
	if len(grant.Scopes) != 1 || grant.Scopes[0] != "esi-assets.read_assets.v1" {
		t.Errorf("grant scopes = %v", grant.Scopes)
	}
	if left, _ := s.ListPendingGrants(); len(left) != 0 {
		t.Errorf("pending not cleared after approval: %+v", left)
	}

	// Views.
	if view, err := s.ListActiveGrantsView(); err != nil || len(view) != 1 {
		t.Fatalf("ListActiveGrantsView = %+v, err %v", view, err)
	}
	if forApp, err := s.ListActiveGrantsForApplication(app.ID); err != nil || len(forApp) != 1 {
		t.Fatalf("ListActiveGrantsForApplication = %+v, err %v", forApp, err)
	}
	if all, err := s.ListActiveGrants(); err != nil || len(all) != 1 {
		t.Fatalf("ListActiveGrants = %+v, err %v", all, err)
	}

	// Revoke → gone.
	if err := s.RevokeGrant(grant.ID); err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}
	if _, err := s.GetActiveGrant(app.ID, 100); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetActiveGrant after revoke err = %v, want ErrNotFound", err)
	}
	// Revoking an already-revoked grant is a no-op miss.
	if err := s.RevokeGrant(grant.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second RevokeGrant err = %v, want ErrNotFound", err)
	}
}

func TestDenyAndApproveMissingPendingGrant(t *testing.T) {
	s := newStore(t)
	if err := s.DenyPendingGrant(123); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("DenyPendingGrant(missing) err = %v, want ErrNotFound", err)
	}
	if err := s.ApprovePendingGrant(123); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("ApprovePendingGrant(missing) err = %v, want ErrNotFound", err)
	}
}

// --- app-wide permissions ---

func TestAppPermissionLifecycle(t *testing.T) {
	s := newStore(t)
	app := seedApp(t, s, "MCO")

	has, err := s.HasAppPermission(app.ID, store.PermissionListCharacters)
	if err != nil || has {
		t.Fatalf("HasAppPermission before request = %v, err %v", has, err)
	}

	if err := s.RequestAppPermission(app.ID, store.PermissionListCharacters); err != nil {
		t.Fatalf("RequestAppPermission: %v", err)
	}
	pending, err := s.ListPendingAppPermissions()
	if err != nil || len(pending) != 1 || pending[0].ApplicationName != "MCO" {
		t.Fatalf("ListPendingAppPermissions = %+v, err %v", pending, err)
	}

	if err := s.ApprovePendingAppPermission(pending[0].ID); err != nil {
		t.Fatalf("ApprovePendingAppPermission: %v", err)
	}
	has, err = s.HasAppPermission(app.ID, store.PermissionListCharacters)
	if err != nil || !has {
		t.Fatalf("HasAppPermission after approval = %v, err %v", has, err)
	}
	if left, _ := s.ListPendingAppPermissions(); len(left) != 0 {
		t.Errorf("pending app permission not cleared: %+v", left)
	}

	view, err := s.ListActiveAppPermissionsView()
	if err != nil || len(view) != 1 || view[0].Permission != store.PermissionListCharacters {
		t.Fatalf("ListActiveAppPermissionsView = %+v, err %v", view, err)
	}

	if err := s.RevokeAppPermission(view[0].ID); err != nil {
		t.Fatalf("RevokeAppPermission: %v", err)
	}
	has, _ = s.HasAppPermission(app.ID, store.PermissionListCharacters)
	if has {
		t.Error("HasAppPermission still true after revoke")
	}
}

func TestDenyAndRevokeMissingAppPermission(t *testing.T) {
	s := newStore(t)
	if err := s.DenyPendingAppPermission(55); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("DenyPendingAppPermission(missing) err = %v, want ErrNotFound", err)
	}
	if err := s.RevokeAppPermission(55); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("RevokeAppPermission(missing) err = %v, want ErrNotFound", err)
	}
}
