package resources

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

const (
	// PostgresMaxIdentLen is the default NAMEDATALEN - 1 in PostgreSQL.
	PostgresMaxIdentLen = 63

	// Prefix is prepended to every generated role.
	Prefix = "hoopdev_"

	// DefaultHashLen is the number of hex characters taken from the SHA-256
	// digest. 8 hex chars = 32 bits of entropy, plenty for typical fleets;
	// bump to 12 if you expect millions of roles.
	DefaultHashLen = 8
)

var (
	nonAlnum        = regexp.MustCompile(`[^a-z0-9]+`)
	multiUnderscore = regexp.MustCompile(`_+`)

	ErrEmptyResource = errors.New("resource name must not be empty")
	ErrEmptySuffix   = errors.New("suffix must not be empty")
	ErrSuffixTooLong = errors.New("suffix too long for the 63-byte budget")
)

// generateSecurePostgresRoleName returns a deterministic role name for (resourceName, suffix).
// The same inputs always produce the same output. The hash is derived from
// the full untruncated input, so resources that differ only in their trailing
// characters still produce distinct role names.
func generateSecurePostgresRoleName(resourceName, suffix string) (string, error) {
	if strings.TrimSpace(resourceName) == "" {
		return "", ErrEmptyResource
	}
	if strings.TrimSpace(suffix) == "" {
		return "", ErrEmptySuffix
	}
	if DefaultHashLen < 4 || DefaultHashLen > 64 {
		return "", fmt.Errorf("hashLen must be between 4 and 64, got %d", DefaultHashLen)
	}

	// Hash the FULL original input — this protects against tail collisions
	// when slugs get truncated. The colon separator prevents ambiguity
	// between ("foo", "bar_baz") and ("foo_bar", "baz").
	sum := sha256.Sum256([]byte(resourceName + ":" + suffix))
	digest := hex.EncodeToString(sum[:])[:DefaultHashLen]

	cleanSuffix := slugify(suffix)
	if cleanSuffix == "" {
		return "", ErrEmptySuffix
	}

	slugBudget := PostgresMaxIdentLen - len(Prefix) - 1 - len(cleanSuffix) - 1 - len(digest)
	if slugBudget < 1 {
		return "", ErrSuffixTooLong
	}

	slug := slugify(resourceName)
	if len(slug) > slugBudget {
		slug = slug[:slugBudget]
	}
	slug = strings.TrimRight(slug, "_")
	if slug == "" {
		// Pathological input (e.g., a resource name made entirely of
		// punctuation). Fall back to a stable marker so the role is
		// still valid and unique via the hash.
		slug = "x"
	}

	return fmt.Sprintf("%s%s_%s_%s", Prefix, slug, cleanSuffix, digest), nil
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = nonAlnum.ReplaceAllString(s, "_")
	s = multiUnderscore.ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

func upsertConnection(ctx *storagev2.Context, conn *models.Connection) error {
	existentConn, err := models.GetConnectionByName(models.DB, conn.Name)
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}

	if existentConn == nil {
		_, err = models.UpsertConnection(ctx, conn)
		return err
	}

	// sync only password
	if isSet := conn.Envs["envvar:PASS"] != ""; isSet {
		existentConn.Envs = conn.Envs
		_, err = models.UpsertConnection(ctx, existentConn)
		return err
	}
	return nil
}

func b64enc(v string) string { return base64.StdEncoding.EncodeToString([]byte(v)) }
