package krb5

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	codectypes "github.com/hoophq/libhoop/v2/codec/types"
	"github.com/jcmturner/gofork/encoding/asn1"
	"github.com/jcmturner/gokrb5/v8/asn1tools"
	"github.com/jcmturner/gokrb5/v8/client"
	"github.com/jcmturner/gokrb5/v8/credentials"
	"github.com/jcmturner/gokrb5/v8/crypto"
	"github.com/jcmturner/gokrb5/v8/gssapi"
	"github.com/jcmturner/gokrb5/v8/iana/chksumtype"
	"github.com/jcmturner/gokrb5/v8/iana/flags"
	"github.com/jcmturner/gokrb5/v8/iana/keyusage"
	"github.com/jcmturner/gokrb5/v8/messages"
	"github.com/jcmturner/gokrb5/v8/spnego"
	"github.com/jcmturner/gokrb5/v8/types"
)

// initiator implements codectypes.GSSInitiator over one gokrb5 client. One
// Initiate is one upstream login: it fetches the service ticket and builds
// the context that speaks for it.
type initiator struct {
	client *client.Client
}

// Initiate obtains a service ticket for spn (a TGT first, when the client
// has none yet) and returns a context whose first Step yields the initial
// token. gokrb5 takes no context, so the KDC exchange runs on its own
// goroutine and ctx bounds the wait; gokrb5's own socket timeouts end the
// goroutine after an abandoned wait.
func (i *initiator) Initiate(ctx context.Context, spn string) (codectypes.GSSContext, error) {
	type ticket struct {
		tkt messages.Ticket
		key types.EncryptionKey
		err error
	}
	ch := make(chan ticket, 1)
	go func() {
		if err := i.client.AffirmLogin(); err != nil {
			ch <- ticket{err: err}
			return
		}
		tkt, key, err := i.client.GetServiceTicket(spn)
		ch <- ticket{tkt: tkt, key: key, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("kerberos: service ticket for %s: %w", spn, ctx.Err())
	case t := <-ch:
		if t.err != nil {
			return nil, fmt.Errorf("kerberos: service ticket for %s: %w", spn, t.err)
		}
		return newContext(i.client.Credentials, t.tkt, t.key)
	}
}

// gssContext is one SPNEGO exchange: the NegTokenInit carrying an AP-REQ
// with mutual authentication required, then the server's NegTokenResp
// carrying the AP-REP that proves it holds the service key. SPNEGO rather
// than the bare krb5 mechanism because SQL Server speaks only SPNEGO and a
// Postgres built on MIT or Heimdal accepts it through the mechglue.
type gssContext struct {
	key  types.EncryptionKey
	auth types.Authenticator
	init []byte
	sent bool
	done bool
}

// contextFlags are the RFC 4121 checksum flags the initiator requests.
// Mutual is what makes the server answer with an AP-REP; without it a
// forged server could accept the ticket and never prove itself.
const contextFlags = gssapi.ContextFlagMutual | gssapi.ContextFlagInteg | gssapi.ContextFlagConf

var (
	tokIDAPReq = []byte{0x01, 0x00}
	tokIDAPRep = []byte{0x02, 0x00}
)

func newContext(creds *credentials.Credentials, tkt messages.Ticket, key types.EncryptionKey) (*gssContext, error) {
	auth, err := types.NewAuthenticator(creds.Domain(), creds.CName())
	if err != nil {
		return nil, fmt.Errorf("kerberos: authenticator: %w", err)
	}
	auth.Cksum = types.Checksum{CksumType: chksumtype.GSSAPI, Checksum: gssChecksum(contextFlags)}
	apReq, err := messages.NewAPReq(tkt, key, auth)
	if err != nil {
		return nil, fmt.Errorf("kerberos: ap-req: %w", err)
	}
	types.SetFlag(&apReq.APOptions, flags.APOptionMutualRequired)
	apReqBytes, err := apReq.Marshal()
	if err != nil {
		return nil, fmt.Errorf("kerberos: ap-req: %w", err)
	}
	tok := spnego.SPNEGOToken{
		Init: true,
		NegTokenInit: spnego.NegTokenInit{
			MechTypes:      []asn1.ObjectIdentifier{gssapi.OIDKRB5.OID()},
			MechTokenBytes: krb5Token(tokIDAPReq, apReqBytes),
		},
	}
	init, err := tok.Marshal()
	if err != nil {
		return nil, fmt.Errorf("kerberos: negtokeninit: %w", err)
	}
	return &gssContext{key: key, auth: auth, init: init}, nil
}

// Step runs the exchange. The first call carries no peer token and returns
// the NegTokenInit; the second carries the server's reply and, when the
// AP-REP inside it matches the authenticator, reports the context done
// with no token to send. Any other shape is an error: a server that skips
// mutual authentication or asks for a further round is not one this
// initiator can trust.
func (c *gssContext) Step(in []byte) ([]byte, bool, error) {
	switch {
	case c.done:
		return nil, true, errors.New("kerberos: context is already established")
	case !c.sent:
		if len(in) != 0 {
			return nil, false, errors.New("kerberos: the first step carries no peer token")
		}
		c.sent = true
		return c.init, false, nil
	}
	if err := c.verifyReply(in); err != nil {
		return nil, false, err
	}
	c.done = true
	return nil, true, nil
}

// verifyReply accepts the server's token as a SPNEGO NegTokenResp or, from
// a server that answered the krb5 mechanism directly, a bare krb5 token.
func (c *gssContext) verifyReply(in []byte) error {
	if len(in) == 0 {
		return errors.New("kerberos: server sent no token for mutual authentication")
	}
	var neg spnego.SPNEGOToken
	if err := neg.Unmarshal(in); err != nil {
		var mech spnego.KRB5Token
		if mechErr := mech.Unmarshal(in); mechErr != nil {
			return fmt.Errorf("kerberos: server token: %w", err)
		}
		return c.verifyMechToken(mech)
	}
	if !neg.Resp {
		return errors.New("kerberos: server sent a NegTokenInit")
	}
	resp := neg.NegTokenResp
	switch resp.State() {
	case spnego.NegStateAcceptCompleted:
	case spnego.NegStateReject:
		return errors.New("kerberos: server rejected the negotiation")
	default:
		return fmt.Errorf("kerberos: unsupported negotiation state %d", resp.State())
	}
	if len(resp.SupportedMech) > 0 && !resp.SupportedMech.Equal(gssapi.OIDKRB5.OID()) && !resp.SupportedMech.Equal(gssapi.OIDMSLegacyKRB5.OID()) {
		return fmt.Errorf("kerberos: server chose mechanism %s", resp.SupportedMech)
	}
	if len(resp.ResponseToken) == 0 {
		return errors.New("kerberos: server completed without mutual authentication")
	}
	var mech spnego.KRB5Token
	if err := mech.Unmarshal(resp.ResponseToken); err != nil {
		return fmt.Errorf("kerberos: server mechanism token: %w", err)
	}
	return c.verifyMechToken(mech)
}

func (c *gssContext) verifyMechToken(mech spnego.KRB5Token) error {
	switch {
	case mech.IsKRBError():
		return fmt.Errorf("kerberos: server error: %w", mech.KRBError)
	case mech.IsAPRep():
		return c.verifyAPRep(mech.APRep)
	}
	return errors.New("kerberos: server token is not an ap-rep")
}

// verifyAPRep decrypts the AP-REP with the ticket's session key and checks
// it echoes the authenticator's time, which is what proves the server
// holds the service key and answered this exchange.
func (c *gssContext) verifyAPRep(rep messages.APRep) error {
	plain, err := crypto.DecryptEncPart(rep.EncPart, c.key, keyusage.AP_REP_ENCPART)
	if err != nil {
		return fmt.Errorf("kerberos: ap-rep: %w", err)
	}
	var part messages.EncAPRepPart
	if err := part.Unmarshal(plain); err != nil {
		return fmt.Errorf("kerberos: ap-rep: %w", err)
	}
	if part.CTime.Unix() != c.auth.CTime.Unix() || part.Cusec != c.auth.Cusec {
		return errors.New("kerberos: ap-rep does not answer this authenticator")
	}
	return nil
}

// krb5Token wraps a Kerberos message as an RFC 4121 InitialContextToken:
// the krb5 mechanism OID, the two-byte token id, the message, under
// application tag 0.
func krb5Token(tokID, msg []byte) []byte {
	b, _ := asn1.Marshal(gssapi.OIDKRB5.OID())
	b = append(b, tokID...)
	b = append(b, msg...)
	return asn1tools.AddASNAppTag(b, 0)
}

// gssChecksum is the RFC 4121 section 4.1.1 authenticator checksum: a
// 16-byte length, 16 zero bytes of channel binding, the flags.
func gssChecksum(flags uint32) []byte {
	b := make([]byte, 24)
	binary.LittleEndian.PutUint32(b[0:4], 16)
	binary.LittleEndian.PutUint32(b[20:24], flags)
	return b
}
