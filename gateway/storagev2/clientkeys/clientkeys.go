package clientkeysstorage

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"

	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

func GetEntity(ctx *storagev2.Context, xtID string) (*types.ClientKey, error) {
	data, err := ctx.GetEntity(xtID)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	var obj types.ClientKey
	if err := edn.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	if obj.AgentMode == "" {
		obj.AgentMode = pb.AgentModeEmbeddedType
	}
	return &obj, nil
}

func GetByName(ctx *storagev2.Context, name string) (*types.ClientKey, error) {
	payload := fmt.Sprintf(`{:query {
		:find [(pull ?c [*])] 
		:in [org name]
		:where [[?c :clientkey/org org]
				[?c :clientkey/name name]
				[?c :clientkey/enabled true]]}
		:in-args [%q %q]}`, ctx.OrgID, name)
	b, err := ctx.Query(payload)
	if err != nil {
		return nil, err
	}

	var clientKey [][]types.ClientKey
	if err := edn.Unmarshal(b, &clientKey); err != nil {
		return nil, err
	}

	if len(clientKey) == 0 {
		return nil, nil
	}

	ck := clientKey[0][0]
	if ck.AgentMode == "" {
		// maintain compatibility with old client keys
		ck.AgentMode = pb.AgentModeEmbeddedType
	}

	return &ck, nil
}

func List(ctx *storagev2.Context) ([]types.ClientKey, error) {
	payload := fmt.Sprintf(`{:query {
		:find [(pull ?c [*])] 
		:in [org]
		:where [[?c :clientkey/org org]
				[?c :clientkey/enabled true]]}
		:in-args [%q]}`, ctx.OrgID)
	b, err := ctx.Query(payload)
	if err != nil {
		return nil, err
	}

	var clientKeyItems [][]types.ClientKey
	if err := edn.Unmarshal(b, &clientKeyItems); err != nil {
		return nil, err
	}

	var itemList []types.ClientKey
	for _, ck := range clientKeyItems {
		if ck[0].AgentMode == "" {
			// maintain compatibility with old client keys
			ck[0].AgentMode = pb.AgentModeEmbeddedType
		}
		itemList = append(itemList, ck[0])
	}

	return itemList, nil
}

func ValidateDSN(store *storagev2.Store, dsn string) (*types.ClientKey, error) {
	secretKeyHash, err := parseSecretKeyHashFromDsn(dsn)
	if err != nil {
		return nil, err
	}
	payload := fmt.Sprintf(`{:query {
		:find [(pull ?c [*])] 
		:in [secretkey-hash]
		:where [[?c :clientkey/dsnhash secretkey-hash]
				[?c :clientkey/enabled true]]}
		:in-args [%q]}`, secretKeyHash)
	b, err := store.Query(payload)
	if err != nil {
		return nil, err
	}

	var clientKey [][]types.ClientKey
	if err := edn.Unmarshal(b, &clientKey); err != nil {
		return nil, err
	}

	if len(clientKey) == 0 {
		return nil, nil
	}
	ck := clientKey[0][0]
	if ck.AgentMode == "" {
		// maintain compatibility with old client keys
		ck.AgentMode = pb.AgentModeEmbeddedType
	}
	return &ck, nil
}

func Put(ctx *storagev2.Context, name, agentMode string, active bool) (*types.ClientKey, string, error) {
	clientkey, err := GetByName(ctx, name)
	if err != nil {
		return nil, "", err
	}
	if clientkey == nil {
		secretKey, secretKeyHash, err := generateSecureRandomKey()
		if err != nil {
			return nil, "", err
		}
		var dsn string
		switch agentMode {
		case pb.AgentModeEmbeddedType:
			// this mode negotiates the grpc url with the api.
			// In the future we may consolidate to use the grpc url instead
			dsn, err = generateDSN(ctx.ApiURL, name, secretKey, pb.AgentModeEmbeddedType)
		case pb.AgentModeDefaultType:
			dsn, err = generateDSN(ctx.GrpcURL, name, secretKey, pb.AgentModeDefaultType)
		default:
			return nil, "", fmt.Errorf("unknown agent mode %q", agentMode)
		}
		if err != nil {
			return nil, "", err
		}
		obj := &types.ClientKey{
			ID:        uuid.NewString(),
			OrgID:     ctx.OrgID,
			Name:      name,
			AgentMode: agentMode,
			DSNHash:   secretKeyHash,
			Active:    active,
		}
		_, err = ctx.Put(obj)
		return obj, dsn, err
	}
	clientkey.Active = active
	_, err = ctx.Put(clientkey)
	return clientkey, "", err
}

func Evict(ctx *storagev2.Context, name string) error {
	clientKey, err := GetByName(ctx, name)
	if err != nil {
		return err
	}
	if clientKey == nil {
		return nil
	}
	agentXTID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("clientkey:%s", name))).String()
	_, err = ctx.Evict(clientKey.ID, agentXTID)
	return err
}

func generateSecureRandomKey() (secretKey, secretKeyHash string, err error) {
	secretRandomBytes := make([]byte, 32)
	_, err = rand.Read(secretRandomBytes)
	if err != nil {
		return "", "", fmt.Errorf("failed generating entropy, err=%v", err)
	}
	h := sha256.New()
	secretKey = base64.RawURLEncoding.EncodeToString(secretRandomBytes)
	if _, err := h.Write([]byte(secretKey)); err != nil {
		return "", "", fmt.Errorf("failed generating secret hash, err=%v", err)
	}
	return secretKey, fmt.Sprintf("%x", h.Sum(nil)), nil
}

func hash256Key(secretKey string) (secret256Hash string, err error) {
	h := sha256.New()
	if _, err := h.Write([]byte(secretKey)); err != nil {
		return "", fmt.Errorf("failed hashing secret key, err=%v", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// <scheme>://<clientkey-name>:<secret-key>@<host>:<port>?mode=<agent-mode>
func generateDSN(targetURL, secretName, secretKey, agentMode string) (string, error) {
	u, err := url.Parse(targetURL)
	if err != nil {
		return "", fmt.Errorf("failed parsing url, reason=%v", err)
	}
	return fmt.Sprintf("%s://%s:%s@%s:%s?mode=%s",
		u.Scheme, secretName, secretKey, u.Hostname(), u.Port(), agentMode), nil
}

func parseSecretKeyHashFromDsn(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("failed parsing dsn, reason=%v", err)
	}
	if u.Query().Get("mode") == "" {
		// keep compatibility with old dsn validation
		return hash256Key(dsn)
	}
	secretKey, ok := u.User.Password()
	if !ok {
		return "", fmt.Errorf("dsn in wrong format")
	}
	return hash256Key(secretKey)
}
