package krb5

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/session"
	"github.com/jcmturner/gofork/encoding/asn1"
	"github.com/jcmturner/gokrb5/v8/asn1tools"
	"github.com/jcmturner/gokrb5/v8/credentials"
	"github.com/jcmturner/gokrb5/v8/crypto"
	"github.com/jcmturner/gokrb5/v8/gssapi"
	"github.com/jcmturner/gokrb5/v8/iana"
	"github.com/jcmturner/gokrb5/v8/iana/asnAppTag"
	"github.com/jcmturner/gokrb5/v8/iana/etypeID"
	"github.com/jcmturner/gokrb5/v8/iana/flags"
	"github.com/jcmturner/gokrb5/v8/iana/keyusage"
	"github.com/jcmturner/gokrb5/v8/iana/msgtype"
	"github.com/jcmturner/gokrb5/v8/iana/nametype"
	"github.com/jcmturner/gokrb5/v8/messages"
	"github.com/jcmturner/gokrb5/v8/spnego"
	"github.com/jcmturner/gokrb5/v8/test/testdata"
	"github.com/jcmturner/gokrb5/v8/types"
)

// fixtures writes gokrb5's test keytab, ccache and krb5.conf for
// testuser1@TEST.GOKRB5 into a directory and returns the paths.
type fixtures struct{ keytab, ccache, conf string }

func writeFixtures(t *testing.T) fixtures {
	t.Helper()
	dir := t.TempDir()
	f := fixtures{
		keytab: filepath.Join(dir, "svc.keytab"),
		ccache: filepath.Join(dir, "krb5cc"),
		conf:   filepath.Join(dir, "krb5.conf"),
	}
	writeHex(t, f.keytab, testdata.KEYTAB_TESTUSER1_TEST_GOKRB5)
	writeHex(t, f.ccache, testdata.CCACHE_TEST)
	if err := os.WriteFile(f.conf, []byte(testdata.KRB5_CONF), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func writeHex(t *testing.T, path, h string) {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBuildRequiresExactlyOneMaterial(t *testing.T) {
	for name, cfg := range map[string]*daemon.CredentialSourcesConfig{
		"nil":      nil,
		"no block": {},
		"neither":  {Kerberos: &daemon.KerberosSourceConfig{Config: "/x"}},
		"both":     {Kerberos: &daemon.KerberosSourceConfig{Cache: "/c", Keytab: "/k"}},
	} {
		if _, err := build(cfg); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	src, err := build(&daemon.CredentialSourcesConfig{Kerberos: &daemon.KerberosSourceConfig{Keytab: "/k"}})
	if err != nil {
		t.Fatal(err)
	}
	if src.(*source).config != DefaultConfig {
		t.Fatalf("config = %q", src.(*source).config)
	}
}

func TestCredentialFromKeytab(t *testing.T) {
	f := writeFixtures(t)
	src, err := build(&daemon.CredentialSourcesConfig{Kerberos: &daemon.KerberosSourceConfig{Keytab: f.keytab, Config: f.conf}})
	if err != nil {
		t.Fatal(err)
	}
	for _, principal := range []string{"testuser1@TEST.GOKRB5", "testuser1"} {
		cred, err := src.Credential(context.Background(), daemon.CredentialEntry{Name: "svc", Principal: principal}, session.Identity{})
		if err != nil {
			t.Fatalf("%s: %v", principal, err)
		}
		if cred.User != "testuser1@TEST.GOKRB5" || cred.Password != "" || cred.Cert != nil {
			t.Fatalf("%s: credential = %+v", principal, cred)
		}
		if cred.GSS == nil || cred.Release == nil {
			t.Fatalf("%s: no initiator or release", principal)
		}
		if err := cred.Release(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for name, entry := range map[string]daemon.CredentialEntry{
		"no principal":      {Name: "svc"},
		"unknown principal": {Name: "svc", Principal: "nobody@TEST.GOKRB5"},
		"unknown realm":     {Name: "svc", Principal: "testuser1@OTHER.REALM"},
	} {
		_, err := src.Credential(context.Background(), entry, session.Identity{})
		if err == nil || !strings.HasPrefix(err.Error(), `credential "svc": `) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestCredentialFromCCacheRereadsTheFile(t *testing.T) {
	f := writeFixtures(t)
	src, err := build(&daemon.CredentialSourcesConfig{Kerberos: &daemon.KerberosSourceConfig{Cache: f.ccache, Config: f.conf}})
	if err != nil {
		t.Fatal(err)
	}
	cred, err := src.Credential(context.Background(), daemon.CredentialEntry{Name: "svc"}, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if cred.User != "testuser1@TEST.GOKRB5" || cred.GSS == nil {
		t.Fatalf("credential = %+v", cred)
	}
	if _, err := src.Credential(context.Background(), daemon.CredentialEntry{Name: "svc", Principal: "testuser1"}, session.Identity{}); err != nil {
		t.Fatalf("principal without realm: %v", err)
	}
	_, err = src.Credential(context.Background(), daemon.CredentialEntry{Name: "svc", Principal: "other@TEST.GOKRB5"}, session.Identity{})
	if err == nil || !strings.Contains(err.Error(), "holds testuser1@TEST.GOKRB5, the entry names other@TEST.GOKRB5") {
		t.Fatalf("err = %v", err)
	}

	// The platform rotates the file under the running process; the next
	// login must see the new content, here a broken one.
	if err := os.WriteFile(f.ccache, []byte("rotated garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Credential(context.Background(), daemon.CredentialEntry{Name: "svc"}, session.Identity{}); err == nil {
		t.Fatal("want error after the ccache was replaced")
	}
}

func TestCredentialReportsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	src, err := build(&daemon.CredentialSourcesConfig{Kerberos: &daemon.KerberosSourceConfig{Keytab: filepath.Join(dir, "none.keytab"), Config: filepath.Join(dir, "none.conf")}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = src.Credential(context.Background(), daemon.CredentialEntry{Name: "svc", Principal: "x"}, session.Identity{})
	if err == nil || !strings.Contains(err.Error(), "krb5.conf") {
		t.Fatalf("err = %v", err)
	}
}

// exchange is one forged service ticket and the context built on it: what
// Initiate produces after the KDC answered, without a KDC.
type exchange struct {
	key types.EncryptionKey
	ctx *gssContext
}

func newExchange(t *testing.T) exchange {
	t.Helper()
	keyValue := make([]byte, 32)
	if _, err := rand.Read(keyValue); err != nil {
		t.Fatal(err)
	}
	key := types.EncryptionKey{KeyType: etypeID.AES256_CTS_HMAC_SHA1_96, KeyValue: keyValue}
	tkt := messages.Ticket{
		TktVNO:  iana.PVNO,
		Realm:   "TEST.GOKRB5",
		SName:   types.NewPrincipalName(nametype.KRB_NT_SRV_INST, "MSSQLSvc/db.test.gokrb5:1433"),
		EncPart: types.EncryptedData{EType: etypeID.AES256_CTS_HMAC_SHA1_96, KVNO: 2, Cipher: []byte("opaque to the client")},
	}
	ctx, err := newContext(credentials.New("testuser1", "TEST.GOKRB5"), tkt, key)
	if err != nil {
		t.Fatal(err)
	}
	return exchange{key: key, ctx: ctx}
}

// apRep forges the server's answer: an AP-REP under the session key that
// echoes the given time, wrapped as the krb5 mechanism token.
func (e exchange) apRep(t *testing.T, ctime int64, cusec int) []byte {
	t.Helper()
	part := messages.EncAPRepPart{CTime: e.ctx.auth.CTime, Cusec: cusec}
	if ctime != e.ctx.auth.CTime.Unix() {
		part.CTime = e.ctx.auth.CTime.Add(1e9)
	}
	plain, err := asn1.Marshal(part)
	if err != nil {
		t.Fatal(err)
	}
	plain = asn1tools.AddASNAppTag(plain, asnAppTag.EncAPRepPart)
	enc, err := crypto.GetEncryptedData(plain, e.key, keyusage.AP_REP_ENCPART, 0)
	if err != nil {
		t.Fatal(err)
	}
	rep := messages.APRep{PVNO: iana.PVNO, MsgType: msgtype.KRB_AP_REP, EncPart: enc}
	b, err := asn1.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	return krb5Token(tokIDAPRep, asn1tools.AddASNAppTag(b, asnAppTag.APREP))
}

func negTokenResp(t *testing.T, state spnego.NegState, mech []byte) []byte {
	t.Helper()
	resp := spnego.NegTokenResp{NegState: asn1.Enumerated(state), SupportedMech: gssapi.OIDKRB5.OID(), ResponseToken: mech}
	b, err := resp.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStepSendsMutualAPReqThenAcceptsAPRep(t *testing.T) {
	e := newExchange(t)
	out, done, err := e.ctx.Step(nil)
	if err != nil || done || len(out) == 0 {
		t.Fatalf("first step: out=%d done=%v err=%v", len(out), done, err)
	}

	var tok spnego.SPNEGOToken
	if err := tok.Unmarshal(out); err != nil {
		t.Fatal(err)
	}
	if !tok.Init || len(tok.NegTokenInit.MechTypes) != 1 || !tok.NegTokenInit.MechTypes[0].Equal(gssapi.OIDKRB5.OID()) {
		t.Fatalf("not a krb5 NegTokenInit: %+v", tok)
	}
	var mech spnego.KRB5Token
	if err := mech.Unmarshal(tok.NegTokenInit.MechTokenBytes); err != nil {
		t.Fatal(err)
	}
	if !mech.IsAPReq() || !types.IsFlagSet(&mech.APReq.APOptions, flags.APOptionMutualRequired) {
		t.Fatal("ap-req does not require mutual authentication")
	}
	if err := mech.APReq.DecryptAuthenticator(e.key); err != nil {
		t.Fatal(err)
	}
	auth := mech.APReq.Authenticator
	if auth.CName.PrincipalNameString() != "testuser1" || auth.CRealm != "TEST.GOKRB5" {
		t.Fatalf("authenticator names %s@%s", auth.CName.PrincipalNameString(), auth.CRealm)
	}
	if string(auth.Cksum.Checksum) != string(gssChecksum(contextFlags)) {
		t.Fatalf("checksum = %x", auth.Cksum.Checksum)
	}

	out, done, err = e.ctx.Step(negTokenResp(t, spnego.NegStateAcceptCompleted, e.apRep(t, e.ctx.auth.CTime.Unix(), e.ctx.auth.Cusec)))
	if err != nil || !done || len(out) != 0 {
		t.Fatalf("second step: out=%d done=%v err=%v", len(out), done, err)
	}
	if _, _, err := e.ctx.Step(nil); err == nil {
		t.Fatal("a third step must fail")
	}
}

func TestStepAcceptsBareKRB5Reply(t *testing.T) {
	e := newExchange(t)
	if _, _, err := e.ctx.Step(nil); err != nil {
		t.Fatal(err)
	}
	_, done, err := e.ctx.Step(e.apRep(t, e.ctx.auth.CTime.Unix(), e.ctx.auth.Cusec))
	if err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
}

func TestStepRefusesBadReplies(t *testing.T) {
	cases := map[string]func(t *testing.T, e exchange) []byte{
		"wrong cusec": func(t *testing.T, e exchange) []byte {
			return negTokenResp(t, spnego.NegStateAcceptCompleted, e.apRep(t, e.ctx.auth.CTime.Unix(), e.ctx.auth.Cusec+1))
		},
		"wrong ctime": func(t *testing.T, e exchange) []byte {
			return negTokenResp(t, spnego.NegStateAcceptCompleted, e.apRep(t, e.ctx.auth.CTime.Unix()+1, e.ctx.auth.Cusec))
		},
		"wrong key": func(t *testing.T, e exchange) []byte {
			other := newExchange(t)
			return negTokenResp(t, spnego.NegStateAcceptCompleted, other.apRep(t, other.ctx.auth.CTime.Unix(), other.ctx.auth.Cusec))
		},
		"no mutual": func(t *testing.T, e exchange) []byte {
			return negTokenResp(t, spnego.NegStateAcceptCompleted, nil)
		},
		"rejected": func(t *testing.T, e exchange) []byte {
			return negTokenResp(t, spnego.NegStateReject, nil)
		},
		"incomplete": func(t *testing.T, e exchange) []byte {
			return negTokenResp(t, spnego.NegStateAcceptIncomplete, e.apRep(t, e.ctx.auth.CTime.Unix(), e.ctx.auth.Cusec))
		},
		"empty":   func(*testing.T, exchange) []byte { return nil },
		"garbage": func(*testing.T, exchange) []byte { return []byte{0x30, 0x03, 0x02, 0x01, 0x01} },
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			e := newExchange(t)
			if _, _, err := e.ctx.Step(nil); err != nil {
				t.Fatal(err)
			}
			out, done, err := e.ctx.Step(reply(t, e))
			if err == nil || done || out != nil {
				t.Fatalf("out=%v done=%v err=%v", out, done, err)
			}
		})
	}
}

func TestStepFirstCallCarriesNoToken(t *testing.T) {
	e := newExchange(t)
	if _, _, err := e.ctx.Step([]byte{0xa1}); err == nil {
		t.Fatal("want error")
	}
}

func TestInitiateHonoursContext(t *testing.T) {
	f := writeFixtures(t)
	src, err := build(&daemon.CredentialSourcesConfig{Kerberos: &daemon.KerberosSourceConfig{Keytab: f.keytab, Config: f.conf}})
	if err != nil {
		t.Fatal(err)
	}
	cred, err := src.Credential(context.Background(), daemon.CredentialEntry{Name: "svc", Principal: "testuser1"}, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cred.Release(context.Background()) }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cred.GSS.Initiate(ctx, "MSSQLSvc/db:1433"); err == nil || !strings.Contains(err.Error(), "MSSQLSvc/db:1433") {
		t.Fatalf("err = %v", err)
	}
}

func TestSplitAndComparePrincipals(t *testing.T) {
	if n, r := splitPrincipal("svc/host@REALM"); n != "svc/host" || r != "REALM" {
		t.Fatalf("%q %q", n, r)
	}
	if n, r := splitPrincipal("svc"); n != "svc" || r != "" {
		t.Fatalf("%q %q", n, r)
	}
	if !samePrincipal("svc", "svc@realm", "REALM") || samePrincipal("svc", "svc@OTHER", "REALM") || samePrincipal("a@R", "b@R", "R") {
		t.Fatal("samePrincipal")
	}
}
