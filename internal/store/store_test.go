package store_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/BOS-Development/esi-bridge/internal/db"
	"github.com/BOS-Development/esi-bridge/internal/store"
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

func mustAccount(t *testing.T, s *store.Store, name string) store.Account {
	t.Helper()
	a, err := s.CreateAccount(name)
	if err != nil {
		t.Fatalf("CreateAccount(%q): %v", name, err)
	}
	return a
}

func mustCharacter(t *testing.T, s *store.Store, id, accountID int64, name string, expires time.Time) store.Character {
	t.Helper()
	c := store.Character{
		ID:                   id,
		AccountID:            accountID,
		Name:                 name,
		RefreshTokenEnc:      []byte("refresh-" + name),
		AccessTokenEnc:       []byte("access-" + name),
		AccessTokenExpiresAt: expires,
		Scopes:               []string{"esi-a.v1", "esi-b.v1"},
	}
	if err := s.UpsertCharacter(c); err != nil {
		t.Fatalf("UpsertCharacter: %v", err)
	}
	return c
}

func mustApp(t *testing.T, s *store.Store, name, hash string) store.Application {
	t.Helper()
	a, err := s.CreateApplication(name, hash)
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	return a
}

func TestAccounts(t *testing.T) {
	s := newStore(t)

	b := mustAccount(t, s, "Bravo")
	if b.ID == 0 || b.Name != "Bravo" || b.CreatedAt.IsZero() {
		t.Fatalf("CreateAccount = %+v", b)
	}
	mustAccount(t, s, "Alpha")

	got, err := s.GetAccount(b.ID)
	if err != nil || got != b {
		t.Fatalf("GetAccount = %+v, %v; want %+v", got, err, b)
	}
	if _, err := s.GetAccount(9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetAccount(missing) err = %v, want ErrNotFound", err)
	}

	list, err := s.ListAccounts()
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(list) != 2 || list[0].Name != "Alpha" || list[1].Name != "Bravo" {
		t.Fatalf("ListAccounts = %+v, want sorted Alpha, Bravo", list)
	}
}

func TestTokensUpdatedAtFallsBackToCreatedAt(t *testing.T) {
	conn, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	s := store.New(conn)
	acct := mustAccount(t, s, "Main")
	mustCharacter(t, s, 42, acct.ID, "Zed", time.Now())

	// Simulate a pre-migration row with an empty tokens_updated_at.
	if _, err := conn.Exec(`UPDATE characters SET tokens_updated_at = '' WHERE id = 42`); err != nil {
		t.Fatalf("clear tokens_updated_at: %v", err)
	}
	got, err := s.GetCharacter(42)
	if err != nil {
		t.Fatalf("GetCharacter: %v", err)
	}
	if got.TokensUpdatedAt.IsZero() || !got.TokensUpdatedAt.Equal(got.CreatedAt) {
		t.Fatalf("TokensUpdatedAt = %v, want CreatedAt %v", got.TokensUpdatedAt, got.CreatedAt)
	}
}

func TestGetOrCreateDefaultAccount(t *testing.T) {
	s := newStore(t)

	first, err := s.GetOrCreateDefaultAccount()
	if err != nil {
		t.Fatalf("GetOrCreateDefaultAccount: %v", err)
	}
	if first.Name != store.DefaultAccountName {
		t.Fatalf("Name = %q, want %q", first.Name, store.DefaultAccountName)
	}
	second, err := s.GetOrCreateDefaultAccount()
	if err != nil {
		t.Fatalf("GetOrCreateDefaultAccount (again): %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second call created a new account: %d != %d", second.ID, first.ID)
	}

	other, err := s.GetOrCreateAccountByName("Alts")
	if err != nil || other.ID == first.ID || other.Name != "Alts" {
		t.Fatalf("GetOrCreateAccountByName(Alts) = %+v, %v", other, err)
	}
}

func TestCharacterCRUD(t *testing.T) {
	s := newStore(t)
	acct := mustAccount(t, s, "Main")
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Millisecond)

	c := mustCharacter(t, s, 42, acct.ID, "Zed", expires)
	mustCharacter(t, s, 7, acct.ID, "Amy", expires)

	got, err := s.GetCharacter(42)
	if err != nil {
		t.Fatalf("GetCharacter: %v", err)
	}
	if got.Name != "Zed" || got.AccountID != acct.ID ||
		string(got.RefreshTokenEnc) != string(c.RefreshTokenEnc) ||
		string(got.AccessTokenEnc) != string(c.AccessTokenEnc) ||
		!got.AccessTokenExpiresAt.Equal(expires) ||
		!reflect.DeepEqual(got.Scopes, c.Scopes) {
		t.Fatalf("GetCharacter = %+v", got)
	}
	if got.CreatedAt.IsZero() || got.TokensUpdatedAt.IsZero() {
		t.Fatalf("timestamps not set: %+v", got)
	}

	if _, err := s.GetCharacter(1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetCharacter(missing) err = %v", err)
	}

	// Upsert replaces fields on the existing row.
	c.Name = "Zed Renamed"
	c.Scopes = nil
	if err := s.UpsertCharacter(c); err != nil {
		t.Fatalf("UpsertCharacter (update): %v", err)
	}
	got, _ = s.GetCharacter(42)
	if got.Name != "Zed Renamed" || got.Scopes != nil {
		t.Fatalf("after upsert = %+v", got)
	}

	list, err := s.ListCharacters()
	if err != nil {
		t.Fatalf("ListCharacters: %v", err)
	}
	if len(list) != 2 || list[0].Name != "Amy" || list[1].Name != "Zed Renamed" {
		t.Fatalf("ListCharacters = %+v", list)
	}

	if err := s.DeleteCharacter(42); err != nil {
		t.Fatalf("DeleteCharacter: %v", err)
	}
	if err := s.DeleteCharacter(42); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DeleteCharacter(again) err = %v", err)
	}
}

func TestUpsertCharacterRequiresAccount(t *testing.T) {
	s := newStore(t)
	err := s.UpsertCharacter(store.Character{ID: 1, AccountID: 999, Name: "Orphan"})
	if err == nil {
		t.Fatal("expected foreign key error for unknown account")
	}
}

func TestUpdateCharacterTokens(t *testing.T) {
	s := newStore(t)
	acct := mustAccount(t, s, "Main")
	mustCharacter(t, s, 42, acct.ID, "Zed", time.Now())

	newExpiry := time.Now().Add(20 * time.Minute).UTC().Truncate(time.Millisecond)
	if err := s.SetCharacterReauth(42, "revoked"); err != nil {
		t.Fatalf("SetCharacterReauth: %v", err)
	}
	if err := s.UpdateCharacterTokens(42, []byte("r2"), []byte("a2"), store.FormatTime(newExpiry), "client-x"); err != nil {
		t.Fatalf("UpdateCharacterTokens: %v", err)
	}
	got, _ := s.GetCharacter(42)
	if string(got.RefreshTokenEnc) != "r2" || string(got.AccessTokenEnc) != "a2" || !got.AccessTokenExpiresAt.Equal(newExpiry) {
		t.Fatalf("after update = %+v", got)
	}
	if got.ClientID != "client-x" {
		t.Errorf("ClientID = %q, want client-x", got.ClientID)
	}
	if got.ReauthReason != "" {
		t.Errorf("ReauthReason = %q, want it cleared by a successful refresh", got.ReauthReason)
	}

	if err := s.UpdateCharacterTokens(1, nil, nil, store.FormatTime(newExpiry), ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateCharacterTokens(missing) err = %v", err)
	}
}

func TestListCharactersExpiringBefore(t *testing.T) {
	s := newStore(t)
	acct := mustAccount(t, s, "Main")
	now := time.Now()
	mustCharacter(t, s, 1, acct.ID, "Soon", now.Add(time.Minute))
	mustCharacter(t, s, 2, acct.ID, "Expired", now.Add(-time.Hour))
	mustCharacter(t, s, 3, acct.ID, "Later", now.Add(time.Hour))

	due, err := s.ListCharactersExpiringBefore(store.FormatTime(now.Add(5 * time.Minute)))
	if err != nil {
		t.Fatalf("ListCharactersExpiringBefore: %v", err)
	}
	ids := map[int64]bool{}
	for _, c := range due {
		ids[c.ID] = true
	}
	if len(due) != 2 || !ids[1] || !ids[2] {
		t.Fatalf("due = %+v, want characters 1 and 2", due)
	}

	// A character waiting on re-authorization is never due for refresh.
	if err := s.SetCharacterReauth(2, "revoked"); err != nil {
		t.Fatalf("SetCharacterReauth: %v", err)
	}
	due, err = s.ListCharactersExpiringBefore(store.FormatTime(now.Add(5 * time.Minute)))
	if err != nil {
		t.Fatalf("ListCharactersExpiringBefore: %v", err)
	}
	if len(due) != 1 || due[0].ID != 1 {
		t.Fatalf("due = %+v, want only character 1", due)
	}
}

func TestCharacterReauthFlag(t *testing.T) {
	s := newStore(t)
	acct := mustAccount(t, s, "Main")
	mustCharacter(t, s, 1, acct.ID, "A", time.Now())
	mustCharacter(t, s, 2, acct.ID, "B", time.Now())

	if err := s.SetCharacterReauth(1, "grant revoked"); err != nil {
		t.Fatalf("SetCharacterReauth: %v", err)
	}
	if err := s.SetCharacterReauth(2, "grant revoked"); err != nil {
		t.Fatalf("SetCharacterReauth: %v", err)
	}
	if got, _ := s.GetCharacter(1); got.ReauthReason != "grant revoked" {
		t.Fatalf("ReauthReason = %q", got.ReauthReason)
	}
	if err := s.SetCharacterReauth(999, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetCharacterReauth(missing) err = %v, want ErrNotFound", err)
	}

	// Logging in again (an upsert) clears the flag and records the client.
	c := mustCharacter(t, s, 1, acct.ID, "A", time.Now())
	c.ClientID = "client-y"
	if err := s.UpsertCharacter(c); err != nil {
		t.Fatalf("UpsertCharacter: %v", err)
	}
	got, _ := s.GetCharacter(1)
	if got.ReauthReason != "" || got.ClientID != "client-y" {
		t.Fatalf("after re-login: ReauthReason = %q, ClientID = %q", got.ReauthReason, got.ClientID)
	}

	if err := s.ClearAllCharacterReauth(); err != nil {
		t.Fatalf("ClearAllCharacterReauth: %v", err)
	}
	if got, _ := s.GetCharacter(2); got.ReauthReason != "" {
		t.Fatalf("ReauthReason after clear-all = %q", got.ReauthReason)
	}
}

func TestApplications(t *testing.T) {
	s := newStore(t)
	app := mustApp(t, s, "Tool", "hash-1")
	if app.ID == 0 || app.Name != "Tool" || app.APIKeyHash != "hash-1" || app.CreatedAt.IsZero() {
		t.Fatalf("CreateApplication = %+v", app)
	}

	got, err := s.GetApplication(app.ID)
	if err != nil || got != app {
		t.Fatalf("GetApplication = %+v, %v", got, err)
	}
	got, err = s.GetApplicationByKeyHash("hash-1")
	if err != nil || got != app {
		t.Fatalf("GetApplicationByKeyHash = %+v, %v", got, err)
	}
	if _, err := s.GetApplication(999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetApplication(missing) err = %v", err)
	}
	if _, err := s.GetApplicationByKeyHash("nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetApplicationByKeyHash(missing) err = %v", err)
	}
}

func TestGrantLifecycle(t *testing.T) {
	s := newStore(t)
	acct := mustAccount(t, s, "Main")
	mustCharacter(t, s, 42, acct.ID, "Zed", time.Now().Add(time.Hour))
	app := mustApp(t, s, "Tool", "hash")

	if _, err := s.GetActiveGrant(app.ID, 42); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetActiveGrant before approval err = %v", err)
	}

	scopes := []string{"esi-a.v1", "esi-b.v1"}
	if err := s.UpsertPendingGrant(app.ID, 42, scopes); err != nil {
		t.Fatalf("UpsertPendingGrant: %v", err)
	}
	// EnsurePendingGrant must not wipe out the scopes already requested.
	if err := s.EnsurePendingGrant(app.ID, 42); err != nil {
		t.Fatalf("EnsurePendingGrant: %v", err)
	}

	pending, err := s.ListPendingGrants()
	if err != nil {
		t.Fatalf("ListPendingGrants: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %+v, want 1", pending)
	}
	p := pending[0]
	if p.ApplicationName != "Tool" || p.CharacterName != "Zed" || !reflect.DeepEqual(p.RequestedScopes, scopes) || p.RequestedAt.IsZero() {
		t.Fatalf("pending[0] = %+v", p)
	}

	if err := s.ApprovePendingGrant(p.ID); err != nil {
		t.Fatalf("ApprovePendingGrant: %v", err)
	}
	if err := s.ApprovePendingGrant(p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ApprovePendingGrant(again) err = %v", err)
	}
	if pending, _ := s.ListPendingGrants(); len(pending) != 0 {
		t.Fatalf("pending after approve = %+v", pending)
	}

	g, err := s.GetActiveGrant(app.ID, 42)
	if err != nil {
		t.Fatalf("GetActiveGrant: %v", err)
	}
	if !reflect.DeepEqual(g.Scopes, scopes) || g.ApprovedAt.IsZero() || g.RevokedAt != nil {
		t.Fatalf("grant = %+v", g)
	}

	all, err := s.ListActiveGrants()
	if err != nil || len(all) != 1 || all[0].ID != g.ID {
		t.Fatalf("ListActiveGrants = %+v, %v", all, err)
	}
	views, err := s.ListActiveGrantsView()
	if err != nil || len(views) != 1 || views[0].ApplicationName != "Tool" || views[0].CharacterName != "Zed" {
		t.Fatalf("ListActiveGrantsView = %+v, %v", views, err)
	}
	forApp, err := s.ListActiveGrantsForApplication(app.ID)
	if err != nil || len(forApp) != 1 {
		t.Fatalf("ListActiveGrantsForApplication = %+v, %v", forApp, err)
	}
	if other, _ := s.ListActiveGrantsForApplication(app.ID + 1); len(other) != 0 {
		t.Fatalf("grants for another app = %+v", other)
	}

	if err := s.UpdateGrantScopes(g.ID, []string{"esi-a.v1"}); err != nil {
		t.Fatalf("UpdateGrantScopes: %v", err)
	}
	g, _ = s.GetActiveGrant(app.ID, 42)
	if !reflect.DeepEqual(g.Scopes, []string{"esi-a.v1"}) {
		t.Fatalf("scopes after update = %v", g.Scopes)
	}

	if err := s.RevokeGrant(g.ID); err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}
	if err := s.RevokeGrant(g.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RevokeGrant(again) err = %v", err)
	}
	if err := s.UpdateGrantScopes(g.ID, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateGrantScopes(revoked) err = %v", err)
	}
	if _, err := s.GetActiveGrant(app.ID, 42); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetActiveGrant after revoke err = %v", err)
	}

	// Re-approving a revoked grant reactivates it.
	if err := s.UpsertPendingGrant(app.ID, 42, []string{"esi-c.v1"}); err != nil {
		t.Fatalf("UpsertPendingGrant (again): %v", err)
	}
	pending, _ = s.ListPendingGrants()
	if err := s.ApprovePendingGrant(pending[0].ID); err != nil {
		t.Fatalf("ApprovePendingGrant (re-approve): %v", err)
	}
	g, err = s.GetActiveGrant(app.ID, 42)
	if err != nil || !reflect.DeepEqual(g.Scopes, []string{"esi-c.v1"}) {
		t.Fatalf("re-approved grant = %+v, %v", g, err)
	}
}

func TestEnsurePendingGrantCreatesEmptyRequest(t *testing.T) {
	s := newStore(t)
	acct := mustAccount(t, s, "Main")
	mustCharacter(t, s, 42, acct.ID, "Zed", time.Now())
	app := mustApp(t, s, "Tool", "hash")

	if err := s.EnsurePendingGrant(app.ID, 42); err != nil {
		t.Fatalf("EnsurePendingGrant: %v", err)
	}
	pending, _ := s.ListPendingGrants()
	if len(pending) != 1 || pending[0].RequestedScopes != nil {
		t.Fatalf("pending = %+v", pending)
	}

	// A later explicit request does overwrite the scopes.
	if err := s.UpsertPendingGrant(app.ID, 42, []string{"esi-a.v1"}); err != nil {
		t.Fatalf("UpsertPendingGrant: %v", err)
	}
	pending, _ = s.ListPendingGrants()
	if len(pending) != 1 || !reflect.DeepEqual(pending[0].RequestedScopes, []string{"esi-a.v1"}) {
		t.Fatalf("pending after upsert = %+v", pending)
	}
}

func TestDenyPendingGrant(t *testing.T) {
	s := newStore(t)
	acct := mustAccount(t, s, "Main")
	mustCharacter(t, s, 42, acct.ID, "Zed", time.Now())
	app := mustApp(t, s, "Tool", "hash")

	if err := s.UpsertPendingGrant(app.ID, 42, []string{"esi-a.v1"}); err != nil {
		t.Fatalf("UpsertPendingGrant: %v", err)
	}
	pending, _ := s.ListPendingGrants()
	if err := s.DenyPendingGrant(pending[0].ID); err != nil {
		t.Fatalf("DenyPendingGrant: %v", err)
	}
	if err := s.DenyPendingGrant(pending[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DenyPendingGrant(again) err = %v", err)
	}
	if _, err := s.GetActiveGrant(app.ID, 42); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("denied request produced a grant: %v", err)
	}
}

func TestDeleteCharacterCascadesGrants(t *testing.T) {
	s := newStore(t)
	acct := mustAccount(t, s, "Main")
	mustCharacter(t, s, 42, acct.ID, "Zed", time.Now())
	mustCharacter(t, s, 43, acct.ID, "Yan", time.Now())
	app := mustApp(t, s, "Tool", "hash")

	if err := s.UpsertPendingGrant(app.ID, 42, []string{"esi-a.v1"}); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.ListPendingGrants()
	if err := s.ApprovePendingGrant(pending[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPendingGrant(app.ID, 43, []string{"esi-a.v1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCharacter(42); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCharacter(43); err != nil {
		t.Fatal(err)
	}

	if grants, _ := s.ListActiveGrants(); len(grants) != 0 {
		t.Fatalf("grants survived character delete: %+v", grants)
	}
	if pending, _ := s.ListPendingGrants(); len(pending) != 0 {
		t.Fatalf("pending grants survived character delete: %+v", pending)
	}
}

func TestAppPermissionLifecycle(t *testing.T) {
	s := newStore(t)
	app := mustApp(t, s, "Tool", "hash")
	perm := store.PermissionListCharacters

	if ok, err := s.HasAppPermission(app.ID, perm); err != nil || ok {
		t.Fatalf("HasAppPermission before request = %v, %v", ok, err)
	}

	if err := s.RequestAppPermission(app.ID, perm); err != nil {
		t.Fatalf("RequestAppPermission: %v", err)
	}
	// Repeating the request refreshes the row rather than duplicating it.
	if err := s.RequestAppPermission(app.ID, perm); err != nil {
		t.Fatalf("RequestAppPermission (again): %v", err)
	}
	pending, err := s.ListPendingAppPermissions()
	if err != nil {
		t.Fatalf("ListPendingAppPermissions: %v", err)
	}
	if len(pending) != 1 || pending[0].ApplicationName != "Tool" || pending[0].Permission != perm || pending[0].RequestedAt.IsZero() {
		t.Fatalf("pending = %+v", pending)
	}

	if err := s.ApprovePendingAppPermission(pending[0].ID); err != nil {
		t.Fatalf("ApprovePendingAppPermission: %v", err)
	}
	if err := s.ApprovePendingAppPermission(pending[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ApprovePendingAppPermission(again) err = %v", err)
	}
	if ok, err := s.HasAppPermission(app.ID, perm); err != nil || !ok {
		t.Fatalf("HasAppPermission after approve = %v, %v", ok, err)
	}
	if ok, _ := s.HasAppPermission(app.ID, store.PermissionListAccounts); ok {
		t.Fatal("approving one permission granted another")
	}

	views, err := s.ListActiveAppPermissionsView()
	if err != nil || len(views) != 1 || views[0].ApplicationName != "Tool" || views[0].Permission != perm || views[0].ApprovedAt.IsZero() {
		t.Fatalf("ListActiveAppPermissionsView = %+v, %v", views, err)
	}

	if err := s.RevokeAppPermission(views[0].ID); err != nil {
		t.Fatalf("RevokeAppPermission: %v", err)
	}
	if err := s.RevokeAppPermission(views[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RevokeAppPermission(again) err = %v", err)
	}
	if ok, _ := s.HasAppPermission(app.ID, perm); ok {
		t.Fatal("permission still active after revoke")
	}
	if views, _ := s.ListActiveAppPermissionsView(); len(views) != 0 {
		t.Fatalf("active permissions after revoke = %+v", views)
	}

	// Re-approving after revoke reactivates the permission.
	if err := s.RequestAppPermission(app.ID, perm); err != nil {
		t.Fatal(err)
	}
	pending, _ = s.ListPendingAppPermissions()
	if err := s.ApprovePendingAppPermission(pending[0].ID); err != nil {
		t.Fatalf("re-approve: %v", err)
	}
	if ok, _ := s.HasAppPermission(app.ID, perm); !ok {
		t.Fatal("permission not reactivated by re-approval")
	}
}

func TestDenyPendingAppPermission(t *testing.T) {
	s := newStore(t)
	app := mustApp(t, s, "Tool", "hash")
	if err := s.RequestAppPermission(app.ID, store.PermissionListAccounts); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.ListPendingAppPermissions()
	if err := s.DenyPendingAppPermission(pending[0].ID); err != nil {
		t.Fatalf("DenyPendingAppPermission: %v", err)
	}
	if err := s.DenyPendingAppPermission(pending[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DenyPendingAppPermission(again) err = %v", err)
	}
	if ok, _ := s.HasAppPermission(app.ID, store.PermissionListAccounts); ok {
		t.Fatal("denied request produced a permission")
	}
}

func TestKnownAppPermissions(t *testing.T) {
	for _, p := range []string{store.PermissionListCharacters, store.PermissionListAccounts} {
		if !store.KnownAppPermissions[p] {
			t.Errorf("KnownAppPermissions missing %q", p)
		}
	}
	if store.KnownAppPermissions["made_up"] {
		t.Error("KnownAppPermissions accepts an unknown permission")
	}
}

func TestFormatAndParseTime(t *testing.T) {
	loc := time.FixedZone("UTC+5", 5*60*60)
	in := time.Date(2026, 3, 4, 5, 6, 7, 123456789, loc)

	s := store.FormatTime(in)
	if s != "2026-03-04T00:06:07.123456789Z" {
		t.Fatalf("FormatTime = %q", s)
	}
	out, err := store.ParseTimeRFC3339(s)
	if err != nil || !out.Equal(in) {
		t.Fatalf("ParseTimeRFC3339 = %v, %v", out, err)
	}
	if _, err := store.ParseTimeRFC3339("garbage"); err == nil {
		t.Fatal("expected error parsing garbage")
	}
}
