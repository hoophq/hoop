package models_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/gateway/migrations"
	"github.com/hoophq/hoop/gateway/models"
)

const (
	secondOrgID    = "00000000-0000-0000-0000-0000000000a2"
	identityIssuer = "https://container.googleapis.com/v1/projects/p/locations/eu/clusters/eu"
)

func seedSecondOrg(t *testing.T) {
	t.Helper()
	if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'second-org')`, secondOrgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
}

func countSidecars(t *testing.T, name string) int {
	t.Helper()
	var n int
	if err := models.DB.Raw(`SELECT COUNT(*) FROM private.sidecars WHERE org_id = ? AND name = ?`, testOrgID, name).
		Scan(&n).Error; err != nil {
		t.Fatalf("count sidecars: %v", err)
	}
	return n
}

// A sidecar that exists is never created again: concurrent first boots, a
// restart, a new subject and a token-made sidecar all land on one row, and a
// deleted name creates nothing.
func TestGetOrCreateSidecarForIdentity(t *testing.T) {
	startTestDB(t)
	const sub = "system:serviceaccount:ws-1:hoop-sidecar"

	t.Run("concurrent first boots create one row", func(t *testing.T) {
		var wg sync.WaitGroup
		ids := make([]string, 2)
		errs := make([]error, 2)
		for i := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sc, err := models.GetOrCreateSidecarForIdentity(models.DB, testOrgID, "gke-eu-ws-1", identityIssuer, sub, "service-account:"+sub)
				errs[i] = err
				if sc != nil {
					ids[i] = sc.ID
				}
			}()
		}
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("errors: %v, %v", errs[0], errs[1])
		}
		if ids[0] == "" || ids[0] != ids[1] {
			t.Fatalf("both boots must reach one sidecar, got %q and %q", ids[0], ids[1])
		}
		if n := countSidecars(t, "gke-eu-ws-1"); n != 1 {
			t.Fatalf("want 1 row, got %d", n)
		}
		got, err := models.GetSidecarByNameOrID(models.DB, testOrgID, "gke-eu-ws-1")
		if err != nil {
			t.Fatal(err)
		}
		if got.IdentityIssuer == nil || *got.IdentityIssuer != identityIssuer || got.IdentitySubject == nil || *got.IdentitySubject != sub {
			t.Errorf("identity columns not stored: issuer=%v subject=%v", got.IdentityIssuer, got.IdentitySubject)
		}
		if got.CreatedBy != "service-account:"+sub || len(got.Configuration.Listeners) != 0 {
			t.Errorf("want an empty sidecar created by the service account, got created_by=%q listeners=%d",
				got.CreatedBy, len(got.Configuration.Listeners))
		}
		var keyHashIsNull bool
		if err := models.DB.Raw(`SELECT key_hash IS NULL FROM private.sidecars WHERE id = ?`, got.ID).Scan(&keyHashIsNull).Error; err != nil {
			t.Fatal(err)
		}
		if !keyHashIsNull {
			t.Error("a sidecar an identity created has no token")
		}
	})

	t.Run("another subject reaches the row and is recorded", func(t *testing.T) {
		before, err := models.GetSidecarByNameOrID(models.DB, testOrgID, "gke-eu-ws-1")
		if err != nil {
			t.Fatal(err)
		}
		const moved = "system:serviceaccount:ws-1-new:hoop-sidecar"
		sc, err := models.GetOrCreateSidecarForIdentity(models.DB, testOrgID, "gke-eu-ws-1", identityIssuer, moved, "service-account:"+moved)
		if err != nil {
			t.Fatal(err)
		}
		if sc.ID != before.ID || sc.IdentitySubject == nil || *sc.IdentitySubject != moved {
			t.Fatalf("want the same row with the new subject, got id=%s subject=%v", sc.ID, sc.IdentitySubject)
		}
		stored, err := models.GetSidecarByNameOrID(models.DB, testOrgID, "gke-eu-ws-1")
		if err != nil {
			t.Fatal(err)
		}
		if *stored.IdentitySubject != moved || stored.CreatedBy != before.CreatedBy {
			t.Errorf("want subject %q and created_by kept, got %q and %q", moved, *stored.IdentitySubject, stored.CreatedBy)
		}
	})

	t.Run("a sidecar an admin made with a token is reused", func(t *testing.T) {
		const token = "hsc_token_made"
		made := &models.Sidecar{OrgID: testOrgID, Name: "token-made", KeyHash: models.HashAPIKey(token), CreatedBy: "admin@hoop.dev"}
		if err := models.CreateSidecar(models.DB, made); err != nil {
			t.Fatal(err)
		}
		sc, err := models.GetOrCreateSidecarForIdentity(models.DB, testOrgID, "token-made", identityIssuer, sub, "service-account:"+sub)
		if err != nil {
			t.Fatal(err)
		}
		if sc.ID != made.ID || sc.CreatedBy != "admin@hoop.dev" {
			t.Fatalf("want the token-made row, got id=%s created_by=%s", sc.ID, sc.CreatedBy)
		}
		byToken, err := models.GetSidecarByKeyHash(models.DB, models.HashAPIKey(token))
		if err != nil || byToken.ID != made.ID {
			t.Fatalf("the token must keep working: sidecar=%v err=%v", byToken, err)
		}
	})

	t.Run("a long subject fits created_by", func(t *testing.T) {
		long := "system:serviceaccount:ws-1:" + strings.Repeat("a", 300)
		sc, err := models.GetOrCreateSidecarForIdentity(models.DB, testOrgID, "long-subject", identityIssuer, long, "service-account:"+long)
		if err != nil {
			t.Fatal(err)
		}
		if *sc.IdentitySubject != long || len(sc.CreatedBy) != 255 {
			t.Errorf("want the full subject and a 255-character created_by, got %d and %d", len(*sc.IdentitySubject), len(sc.CreatedBy))
		}
	})

	t.Run("a deleted name creates nothing until cleared", func(t *testing.T) {
		deleted, err := models.DeleteSidecarByNameOrID(models.DB, testOrgID, "gke-eu-ws-1")
		if err != nil {
			t.Fatal(err)
		}
		if deleted.Name != "gke-eu-ws-1" || deleted.IdentitySubject == nil {
			t.Fatalf("the delete must return the name and identity, got %+v", deleted)
		}
		if err := models.InsertSidecarDeletedName(models.DB, testOrgID, deleted.Name, "admin@hoop.dev"); err != nil {
			t.Fatal(err)
		}
		_, err = models.GetOrCreateSidecarForIdentity(models.DB, testOrgID, "gke-eu-ws-1", identityIssuer, sub, "service-account:"+sub)
		if !errors.Is(err, models.ErrSidecarNameDeleted) {
			t.Fatalf("want ErrSidecarNameDeleted, got %v", err)
		}
		if n := countSidecars(t, "gke-eu-ws-1"); n != 0 {
			t.Fatalf("a deleted name was created again: %d rows", n)
		}
		names, err := models.ListSidecarDeletedNames(models.DB, testOrgID)
		if err != nil || len(names) != 1 || names[0].DeletedBy != "admin@hoop.dev" {
			t.Fatalf("deleted names = %+v, err=%v", names, err)
		}

		if err := models.DeleteSidecarDeletedName(models.DB, testOrgID, "gke-eu-ws-1"); err != nil {
			t.Fatal(err)
		}
		if err := models.DeleteSidecarDeletedName(models.DB, testOrgID, "gke-eu-ws-1"); !errors.Is(err, models.ErrNotFound) {
			t.Fatalf("clearing twice: want ErrNotFound, got %v", err)
		}
		if _, err := models.GetOrCreateSidecarForIdentity(models.DB, testOrgID, "gke-eu-ws-1", identityIssuer, sub, "service-account:"+sub); err != nil {
			t.Fatalf("a cleared name must be created again: %v", err)
		}
	})

	t.Run("a token-made sidecar returns no identity on delete", func(t *testing.T) {
		made := &models.Sidecar{OrgID: testOrgID, Name: "token-only", KeyHash: models.HashAPIKey("hsc_token_only"), CreatedBy: "admin@hoop.dev"}
		if err := models.CreateSidecar(models.DB, made); err != nil {
			t.Fatal(err)
		}
		deleted, err := models.DeleteSidecarByNameOrID(models.DB, testOrgID, made.ID)
		if err != nil {
			t.Fatal(err)
		}
		if deleted.IdentitySubject != nil {
			t.Errorf("no identity reached this sidecar, got %q", *deleted.IdentitySubject)
		}
	})

	t.Run("a token sidecar needs its key hash", func(t *testing.T) {
		err := models.CreateSidecar(models.DB, &models.Sidecar{OrgID: testOrgID, Name: "no-key", CreatedBy: "admin@hoop.dev"})
		if err == nil {
			t.Fatal("a sidecar with no token and no identity was created")
		}
	})
}

func TestSidecarServiceAccountStore(t *testing.T) {
	startTestDB(t)
	seedSecondOrg(t)

	newSA := func(org, name, pattern string) *models.SidecarServiceAccount {
		return &models.SidecarServiceAccount{OrgID: org, Name: name, Issuer: identityIssuer, Audience: "https://hoop.example.com",
			Claim: "sub", SubjectPattern: pattern, NameTemplate: "gke-eu-{1}", CreatedBy: "admin@hoop.dev"}
	}
	first := newSA(testOrgID, "gke-eu", "system:serviceaccount:*:hoop-sidecar")
	if err := models.CreateSidecarServiceAccount(models.DB, first); err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.CreatedAt.IsZero() || first.JWKS != nil {
		t.Fatalf("want an id, a timestamp and a NULL jwks back, got %+v", first)
	}

	if err := models.CreateSidecarServiceAccount(models.DB, newSA(testOrgID, "gke-eu", "other:*")); !errors.Is(err, models.ErrAlreadyExists) {
		t.Errorf("same name: want ErrAlreadyExists, got %v", err)
	}
	if err := models.CreateSidecarServiceAccount(models.DB, newSA(testOrgID, "gke-eu-2", first.SubjectPattern)); !errors.Is(err, models.ErrAlreadyExists) {
		t.Errorf("same issuer, claim and pattern: want ErrAlreadyExists, got %v", err)
	}

	// Several patterns for one cluster in one organization share the pair.
	extra := newSA(testOrgID, "gke-eu-default", "system:serviceaccount:*:default")
	if err := models.CreateSidecarServiceAccount(models.DB, extra); err != nil {
		t.Fatalf("a second pattern for the same issuer and audience in one organization: %v", err)
	}
	// Another organization on the same issuer and audience could match the
	// first one's subjects, so the pair belongs to the first.
	if err := models.CreateSidecarServiceAccount(models.DB, newSA(secondOrgID, "gke-eu", "system:serviceaccount:*:other")); !errors.Is(err, models.ErrSidecarServiceAccountPairTaken) {
		t.Errorf("another organization, same issuer and audience: want ErrSidecarServiceAccountPairTaken, got %v", err)
	}

	second := newSA(secondOrgID, "gke-eu", first.SubjectPattern)
	second.Audience = "https://hoop.example.com/org-b"
	second.JWKS = []byte(`{"keys": [{"kty": "RSA", "kid": "k1", "n": "AQAB", "e": "AQAB"}]}`)
	if err := models.CreateSidecarServiceAccount(models.DB, second); err != nil {
		t.Fatalf("another organization may use the issuer with its own audience: %v", err)
	}

	byIssuer, err := models.ListSidecarServiceAccountsByIssuer(models.DB, identityIssuer)
	if err != nil {
		t.Fatal(err)
	}
	if len(byIssuer) != 3 || byIssuer[0].ID != first.ID || byIssuer[1].ID != extra.ID || byIssuer[2].ID != second.ID {
		t.Fatalf("want both organizations' mappings, oldest first, got %+v", byIssuer)
	}
	if len(byIssuer[2].JWKS) == 0 || byIssuer[0].JWKS != nil {
		t.Errorf("jwks must read back as stored: %q and %q", byIssuer[0].JWKS, byIssuer[2].JWKS)
	}
	if other, _ := models.ListSidecarServiceAccountsByIssuer(models.DB, "https://other.example.com"); len(other) != 0 {
		t.Errorf("another issuer listed %d mappings", len(other))
	}

	first.Audience = "https://hoop2.example.com"
	updated, err := models.UpdateSidecarServiceAccount(models.DB, first)
	if err != nil || updated.Audience != "https://hoop2.example.com" || updated.CreatedBy != "admin@hoop.dev" {
		t.Fatalf("update: %+v, err=%v", updated, err)
	}
	first.Audience = second.Audience
	if _, err := models.UpdateSidecarServiceAccount(models.DB, first); !errors.Is(err, models.ErrSidecarServiceAccountPairTaken) {
		t.Errorf("update onto another organization's pair: want ErrSidecarServiceAccountPairTaken, got %v", err)
	}
	// A mapping is reachable from its own organization only.
	foreign := *second
	foreign.OrgID, foreign.Audience = testOrgID, "https://unused.example.com"
	if _, err := models.UpdateSidecarServiceAccount(models.DB, &foreign); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("update across organizations: want ErrNotFound, got %v", err)
	}
	if _, err := models.GetSidecarServiceAccount(models.DB, testOrgID, "not-a-uuid"); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("get by a malformed id: want ErrNotFound, got %v", err)
	}
	if err := models.DeleteSidecarServiceAccount(models.DB, testOrgID, second.ID); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("delete across organizations: want ErrNotFound, got %v", err)
	}
	for _, id := range []string{first.ID, extra.ID} {
		if err := models.DeleteSidecarServiceAccount(models.DB, testOrgID, id); err != nil {
			t.Fatal(err)
		}
	}
	if items, _ := models.ListSidecarServiceAccounts(models.DB, testOrgID); len(items) != 0 {
		t.Errorf("want no mapping left, got %d", len(items))
	}
}

// The down migration restores key_hash NOT NULL, so it must remove the
// sidecars only an identity could reach and keep every token-made one.
func TestSidecarServiceAccountMigrationRollsBack(t *testing.T) {
	startTestDB(t)
	tokenMade := &models.Sidecar{OrgID: testOrgID, Name: "token-made", KeyHash: models.HashAPIKey("hsc_rollback"), CreatedBy: "admin@hoop.dev"}
	if err := models.CreateSidecar(models.DB, tokenMade); err != nil {
		t.Fatal(err)
	}
	if _, err := models.GetOrCreateSidecarForIdentity(models.DB, testOrgID, "identity-made", identityIssuer, "sub", "service-account:sub"); err != nil {
		t.Fatal(err)
	}

	down, err := migrations.FS.ReadFile("000130_sidecar_service_accounts.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := models.DB.Exec(string(down)).Error; err != nil {
		t.Fatalf("the down migration failed: %v", err)
	}

	var names []string
	if err := models.DB.Raw(`SELECT name FROM private.sidecars WHERE org_id = ? ORDER BY name`, testOrgID).Scan(&names).Error; err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "token-made" {
		t.Fatalf("want only the token-made sidecar left, got %v", names)
	}
	err = models.DB.Exec(`INSERT INTO private.sidecars (org_id, name, key_hash, created_by) VALUES (?, 'no-key', NULL, 'x')`, testOrgID).Error
	if err == nil {
		t.Error("key_hash accepts NULL after the rollback")
	}
}
