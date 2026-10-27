package services

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/sidecar/daemon"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The RFC 7677 section 3 exchange: user "user", password "pencil". The
// material minted here must verify the proof the RFC's client sent and
// produce the verifier the RFC's server answered.
func TestSCRAMMaterialAgainstRFC7677(t *testing.T) {
	salt, err := base64.StdEncoding.DecodeString("W22ZaJ0SNY7soEsUEjb6gQ==")
	require.NoError(t, err)
	m, err := scramMaterial("pencil", salt, 4096)
	require.NoError(t, err)
	assert.Equal(t, "W22ZaJ0SNY7soEsUEjb6gQ==", m.Salt)
	assert.Equal(t, 4096, m.Iterations)

	const nonce = "rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0"
	authMessage := strings.Join([]string{
		"n=user,r=rOprNGfwEbeRWgbNEkqO",
		"r=" + nonce + ",s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096",
		"c=biws,r=" + nonce,
	}, ",")
	proof, err := base64.StdEncoding.DecodeString("dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ=")
	require.NoError(t, err)
	storedKey, err := base64.StdEncoding.DecodeString(m.StoredKey)
	require.NoError(t, err)
	serverKey, err := base64.StdEncoding.DecodeString(m.ServerKey)
	require.NoError(t, err)

	// Server side of client-final: ClientKey = proof XOR HMAC(StoredKey,
	// AuthMessage), and H(ClientKey) must be StoredKey.
	clientSignature := scramHMAC(storedKey, authMessage)
	clientKey := make([]byte, len(proof))
	for i := range proof {
		clientKey[i] = proof[i] ^ clientSignature[i]
	}
	sum := sha256.Sum256(clientKey)
	assert.True(t, hmac.Equal(sum[:], storedKey), "the RFC's client proof does not verify")

	serverSignature := scramHMAC(serverKey, authMessage)
	assert.Equal(t, "6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4=", base64.StdEncoding.EncodeToString(serverSignature))
}

func TestNewSCRAMMaterialSaltsEachMint(t *testing.T) {
	a, err := NewSCRAMMaterial("hlc_same")
	require.NoError(t, err)
	b, err := NewSCRAMMaterial("hlc_same")
	require.NoError(t, err)
	assert.NotEqual(t, a.Salt, b.Salt)
	assert.NotEqual(t, a.StoredKey, b.StoredKey)
	salt, err := base64.StdEncoding.DecodeString(a.Salt)
	require.NoError(t, err)
	assert.Len(t, salt, 16)
}

const credentialsTestOrgID = "00000000-0000-0000-0000-0000000000c1"

func startCredentialsTestDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'sidecar-credentials-test')`, credentialsTestOrgID).Error)
}

// seedCredentialsUser creates an active user in groups and returns its
// subject, which is neither its id nor its email, as the request context
// carries it.
func seedCredentialsUser(t *testing.T, name string, groups ...string) string {
	t.Helper()
	subject := "idp|" + name
	require.NoError(t, models.DB.Exec(`INSERT INTO private.users (org_id, subject, email, name, status)
		VALUES (?, ?, ?, ?, 'active')`, credentialsTestOrgID, subject, name+"@hoop.dev", name).Error)
	for _, g := range groups {
		require.NoError(t, models.DB.Exec(`INSERT INTO private.user_groups (org_id, user_id, name)
			SELECT ?, id, ? FROM private.users WHERE org_id = ? AND subject = ?`,
			credentialsTestOrgID, g, credentialsTestOrgID, subject).Error)
	}
	return subject
}

func seedCredentialsSidecar(t *testing.T, name string) *models.Sidecar {
	t.Helper()
	sc := &models.Sidecar{
		OrgID:     credentialsTestOrgID,
		Name:      name,
		KeyHash:   models.HashAPIKey("hsc_" + name),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration(daemon.Config{Listeners: []daemon.ListenerConfig{
			{Name: "appdb", Protocol: "postgres", Listen: "127.0.0.1:15432", Upstream: "db:5432"},
		}}),
	}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	return sc
}

func TestComposeSidecarConfigurationCredentialSet(t *testing.T) {
	startCredentialsTestDB(t)
	jane := seedCredentialsUser(t, "jane", "dba")
	sc := seedCredentialsSidecar(t, "pay")
	other := seedCredentialsSidecar(t, "pay-2")

	composed, err := ComposeSidecarConfiguration(models.DB, sc)
	require.NoError(t, err)
	assert.Nil(t, composed.CredentialSet, "no credential, no key")
	doc, err := json.Marshal(composed)
	require.NoError(t, err)
	assert.NotContains(t, string(doc), "credential_set")

	cred, key, err := MintSidecarCredential(models.DB, sc, "appdb", jane, time.Hour)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(key, SidecarCredentialKeyPrefix), key)
	revoked, _, err := MintSidecarCredential(models.DB, sc, "appdb", jane, time.Hour)
	require.NoError(t, err)
	require.NoError(t, models.RevokeSidecarCredential(models.DB, credentialsTestOrgID, revoked.ID))
	_, _, err = MintSidecarCredential(models.DB, other, "appdb", jane, time.Hour)
	require.NoError(t, err)

	composed, err = ComposeSidecarConfiguration(models.DB, sc)
	require.NoError(t, err)
	require.Len(t, composed.CredentialSet, 1)
	entry := composed.CredentialSet[0]
	assert.Equal(t, models.HashAPIKey(key), entry.Hash)
	assert.Equal(t, "idp|jane", entry.Subject)
	assert.Equal(t, "jane@hoop.dev", entry.Email)
	assert.Equal(t, []string{"dba"}, entry.Groups)
	assert.Equal(t, "appdb", entry.Lane)
	assert.WithinDuration(t, cred.ExpireAt, entry.ExpiresAt, time.Second)
	require.NotNil(t, entry.SCRAM)
	assert.Equal(t, cred.ScramSHA256.StoredKey, entry.SCRAM.StoredKey)

	// The served form, as the handshake writes it.
	doc, err = json.Marshal(daemon.ServedForm(composed))
	require.NoError(t, err)
	var served struct {
		CredentialSet []struct {
			Hash  string          `json:"hash"`
			SCRAM json.RawMessage `json:"scram"`
		} `json:"credential_set"`
	}
	require.NoError(t, json.Unmarshal(doc, &served))
	require.Len(t, served.CredentialSet, 1)
	assert.Equal(t, entry.Hash, served.CredentialSet[0].Hash)
	assert.Contains(t, string(served.CredentialSet[0].SCRAM), `"stored_key"`)
	assert.NotContains(t, string(doc), key, "the raw key never leaves the gateway")
}
