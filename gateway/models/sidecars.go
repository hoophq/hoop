package models

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// SidecarConfiguration is the daemon config document stored in the
// sidecars.configuration JSONB column. The Scanner/Valuer pair keeps the JSON
// encoding at the database edge, so every caller above it reads a typed
// config instead of bytes it has to decode itself.
type SidecarConfiguration daemon.Config

func (c SidecarConfiguration) Value() (driver.Value, error) { return json.Marshal(c) }

// Scan decodes into a fresh value and rejects a key the compiled
// daemon.Config does not declare, the same rule the write path and the
// sidecar's own daemon.LoadConfigBytes follow. A row holding a key this
// gateway cannot represent would otherwise be served with that key dropped,
// silently disabling whatever control it named.
func (c *SidecarConfiguration) Scan(value any) error {
	var data []byte
	switch v := value.(type) {
	case nil:
		*c = SidecarConfiguration{}
		return nil
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("failed to scan sidecar configuration, got=%T", value)
	}

	var cfg SidecarConfiguration
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return fmt.Errorf("failed to scan sidecar configuration: %w", err)
	}
	*c = cfg
	return nil
}

type Sidecar struct {
	ID            string               `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	OrgID         string               `gorm:"column:org_id"`
	Name          string               `gorm:"column:name"`
	KeyHash       string               `gorm:"column:key_hash"`
	Configuration SidecarConfiguration `gorm:"column:configuration"`
	CreatedBy     string               `gorm:"column:created_by"`
	CreatedAt     time.Time            `gorm:"column:created_at"`

	// What the sidecar last reported about itself, and what was last served
	// to it. All nullable: a sidecar that has never handshaken and one too
	// old to report must both read as unknown, never as converged.
	LastSeenAt      *time.Time `gorm:"column:last_seen_at"`
	ReportedVersion *string    `gorm:"column:reported_version"`
	ServedRevision  *string    `gorm:"column:served_revision"`
	AppliedRevision *string    `gorm:"column:applied_revision"`
	LastOutcome     *string    `gorm:"column:last_outcome"`
	// LastError is the reason the sidecar gave with a refused or restart
	// outcome. ServedRevisionAt is when ServedRevision last changed, so a
	// document served a moment ago reads apart from one nobody applied.
	LastError        *string    `gorm:"column:last_error"`
	ServedRevisionAt *time.Time `gorm:"column:served_revision_at"`

	// Capabilities is what the last handshake reported in
	// daemon.CapabilitiesHeader. Nil until the sidecar handshakes; empty for
	// a build too old to send the header.
	Capabilities pq.StringArray `gorm:"column:capabilities;type:text[]"`

	// ServedGen is the org's sidecar_config_gens.gen the served document was
	// composed at, and ComposedAt when. A handshake skips the compose while
	// ServedGen still equals OrgConfigGen (migration 000128). NULL until a
	// handshake composes.
	ServedGen  *int64     `gorm:"column:served_gen"`
	ComposedAt *time.Time `gorm:"column:composed_at"`
	// OrgConfigGen is read with the row, never written through it: only
	// GetSidecarByKeyHash selects it, in the same statement as Configuration,
	// so the two cannot come from different writes.
	OrgConfigGen int64 `gorm:"column:org_config_gen;->"`
}

const sidecarColumns = `
	s.id, s.org_id, s.name, s.created_by, s.created_at, s.configuration,
	s.last_seen_at, s.reported_version, s.served_revision, s.applied_revision, s.last_outcome,
	s.last_error, s.served_revision_at, s.capabilities`

func CreateSidecar(db *gorm.DB, s *Sidecar) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	s.CreatedAt = time.Now().UTC()
	err := db.Table("private.sidecars").Create(map[string]any{
		"id":            s.ID,
		"org_id":        s.OrgID,
		"name":          s.Name,
		"key_hash":      s.KeyHash,
		"configuration": s.Configuration,
		"created_by":    s.CreatedBy,
		"created_at":    s.CreatedAt,
	}).Error
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func ListSidecars(db *gorm.DB, orgID string) ([]Sidecar, error) {
	var items []Sidecar
	err := db.Raw(`
	SELECT`+sidecarColumns+`
	FROM private.sidecars s
	WHERE s.org_id = ?
	ORDER BY s.name`, orgID).
		Find(&items).
		Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

func GetSidecarByNameOrID(db *gorm.DB, orgID, nameOrID string) (*Sidecar, error) {
	identifierClause := "s.name = ?"
	if _, err := uuid.Parse(nameOrID); err == nil {
		identifierClause = "s.id = ?"
	}

	var item Sidecar
	err := db.Raw(`
	SELECT`+sidecarColumns+`
	FROM private.sidecars s
	WHERE s.org_id = ? AND `+identifierClause, orgID, nameOrID).
		Scan(&item).
		Error
	if err != nil {
		return nil, err
	}
	if item.ID == "" {
		return nil, ErrNotFound
	}
	return &item, nil
}

// GetSidecarByNameOrIDForUpdate is GetSidecarByNameOrID with the row locked
// until the caller's transaction ends.
func GetSidecarByNameOrIDForUpdate(tx *gorm.DB, orgID, nameOrID string) (*Sidecar, error) {
	identifierClause := "s.name = ?"
	if _, err := uuid.Parse(nameOrID); err == nil {
		identifierClause = "s.id = ?"
	}
	var item Sidecar
	err := tx.Raw(`
	SELECT`+sidecarColumns+`
	FROM private.sidecars s
	WHERE s.org_id = ? AND `+identifierClause+`
	FOR UPDATE OF s`, orgID, nameOrID).
		Scan(&item).
		Error
	if err != nil {
		return nil, err
	}
	if item.ID == "" {
		return nil, ErrNotFound
	}
	return &item, nil
}

// GetSidecarByKeyHash resolves the token holder. It is not org scoped: the
// token identifies the organization.
func GetSidecarByKeyHash(db *gorm.DB, keyHash string) (*Sidecar, error) {
	var item Sidecar
	err := db.Raw(`
	SELECT s.id, s.org_id, s.name, s.created_by, s.created_at, s.configuration,
		s.reported_version, s.served_revision, s.last_outcome, s.capabilities,
		s.served_gen, s.composed_at, COALESCE(g.gen, 0) AS org_config_gen
	FROM private.sidecars s
	LEFT JOIN private.sidecar_config_gens g ON g.org_id = s.org_id
	WHERE s.key_hash = ?`, keyHash).
		Scan(&item).
		Error
	if err != nil {
		return nil, err
	}
	if item.ID == "" {
		return nil, ErrNotFound
	}
	return &item, nil
}

// UpdateSidecarConfiguration replaces the stored config document and returns
// the updated row. Returns ErrNotFound when no row matched.
func UpdateSidecarConfiguration(db *gorm.DB, orgID, nameOrID string, configuration SidecarConfiguration) (*Sidecar, error) {
	identifierClause := "name = ?"
	if _, err := uuid.Parse(nameOrID); err == nil {
		identifierClause = "id = ?"
	}

	var item Sidecar
	err := db.Raw(`
	UPDATE private.sidecars
	SET configuration = ?
	WHERE org_id = ? AND `+identifierClause+`
	RETURNING id, org_id, name, created_by, created_at, configuration, capabilities, reported_version`,
		configuration, orgID, nameOrID).
		Scan(&item).
		Error
	if err != nil {
		return nil, err
	}
	if item.ID == "" {
		return nil, ErrNotFound
	}
	return &item, nil
}

// PatchSidecarConfiguration merges a partial document into the stored
// configuration: the keys in merge overwrite, the rest of the document is left
// as it was. It is a jsonb concatenation on the column rather than a document
// replacement, so a configuration a sidecar imported concurrently keeps its
// listeners instead of being overwritten by a stale copy. removeLoadFromDisk
// drops the key after the merge — false is the same fact as absent, and an
// older sidecar rejects an unknown key. Returns ErrNotFound when no row
// matched.
func PatchSidecarConfiguration(db *gorm.DB, orgID, nameOrID string, merge json.RawMessage, removeLoadFromDisk bool) (*Sidecar, error) {
	identifierClause := "name = ?"
	if _, err := uuid.Parse(nameOrID); err == nil {
		identifierClause = "id = ?"
	}

	expr := "configuration || ?::jsonb"
	if removeLoadFromDisk {
		expr = "(configuration || ?::jsonb) - 'load_from_disk'"
	}

	var item Sidecar
	err := db.Raw(`
	UPDATE private.sidecars
	SET configuration = `+expr+`
	WHERE org_id = ? AND `+identifierClause+`
	RETURNING id, org_id, name, created_by, created_at, configuration, capabilities, reported_version`,
		string(merge), orgID, nameOrID).
		Scan(&item).
		Error
	if err != nil {
		return nil, err
	}
	if item.ID == "" {
		return nil, ErrNotFound
	}
	return &item, nil
}

// ResetSidecarConfigurationTx empties the stored configuration, so the next
// handshake answers 412 and the sidecar imports its config file again.
func ResetSidecarConfigurationTx(tx *gorm.DB, orgID, id string) (*Sidecar, error) {
	var item Sidecar
	err := tx.Raw(`
	UPDATE private.sidecars SET configuration = '{}'::jsonb
	WHERE org_id = ? AND id = ?
	RETURNING id, org_id, name, created_by, created_at, configuration, capabilities, reported_version`, orgID, id).
		Scan(&item).Error
	if err != nil {
		return nil, err
	}
	if item.ID == "" {
		return nil, ErrNotFound
	}
	return &item, nil
}

// AdoptSidecarConfiguration stores the document a sidecar carried locally,
// but only while the row holds no listeners: the guard runs in the UPDATE
// itself, so a configuration authored concurrently in the control plane is
// never overwritten by a restarting sidecar. ErrAlreadyExists reports the
// guard firing; the row is known to exist because the caller authenticated
// its token.
func AdoptSidecarConfiguration(db *gorm.DB, orgID, id string, configuration SidecarConfiguration) (*Sidecar, error) {
	var item Sidecar
	err := db.Raw(`
	UPDATE private.sidecars
	SET configuration = ?
	WHERE org_id = ? AND id = ?
	  AND (jsonb_typeof(configuration->'listeners') IS DISTINCT FROM 'array'
	       OR jsonb_array_length(configuration->'listeners') = 0)
	RETURNING id, org_id, name, created_by, created_at, configuration, capabilities, reported_version`,
		configuration, orgID, id).
		Scan(&item).
		Error
	if err != nil {
		return nil, err
	}
	if item.ID == "" {
		return nil, ErrAlreadyExists
	}
	return &item, nil
}

// DeleteSidecarByNameOrID hard deletes the row and returns its id, so the
// caller can evict any process-local runtime state.
func DeleteSidecarByNameOrID(db *gorm.DB, orgID, nameOrID string) (string, error) {
	identifierClause := "name = ?"
	if _, err := uuid.Parse(nameOrID); err == nil {
		identifierClause = "id = ?"
	}

	var deletedID string
	err := db.Raw(`
	DELETE FROM private.sidecars
	WHERE org_id = ? AND `+identifierClause+`
	RETURNING id`, orgID, nameOrID).
		Scan(&deletedID).
		Error
	if err != nil {
		return "", err
	}
	if deletedID == "" {
		return "", ErrNotFound
	}
	return deletedID, nil
}

// RecordSidecarHandshake stores one handshake: what the sidecar said about
// itself, and the revision of the document being answered with.
//
// One statement for both halves because they describe one exchange. Writing
// them apart would let a crash between the two leave a row claiming the
// sidecar applied a revision that was never served.
//
// Called from the handshake only. The configuration poll deliberately records
// nothing, so a sidecar that polls between handshakes cannot overwrite what it
// last reported about itself.
//
// capabilities is stored as an array even when empty, so a build too old to
// report reads apart from a sidecar that never handshaked.
//
// A refused or restart outcome the sidecar reported is HELD, with its
// revision and reason, through a handshake that reports nothing and through
// one that reports "unchanged". A boot on a refused document exits before it
// can report anything, and a build from before ADR-0022 decays its refusal
// into "unchanged" after one tick; either would otherwise read as converged.
// Any other outcome replaces what is held.
//
// A SidecarOutcomeNotServed is the plane's own verdict, so this handshake,
// which the plane did serve, ends it whatever the sidecar reports: the
// sidecar never received the document it names and holds nothing about it.
//
// composedGen is the org gen the plane composed servedRevision at. Nil keeps
// served_gen and composed_at: the plane skipped the compose and served the
// revision it already recorded.
func RecordSidecarHandshake(db *gorm.DB, sidecarID, version, appliedRevision, lastOutcome, lastError, servedRevision string, capabilities []string, composedGen *int64) error {
	if capabilities == nil {
		capabilities = []string{}
	}
	return db.Exec(`
	UPDATE private.sidecars SET
		last_seen_at = NOW(),
		reported_version = NULLIF(@version, ''),
		applied_revision = CASE WHEN @outcome = '' OR (@outcome = 'unchanged' AND last_outcome IN ('refused', 'restart'))
			THEN applied_revision ELSE NULLIF(@applied, '') END,
		last_error = CASE WHEN last_outcome = @not_served THEN NULLIF(@error, '')
			WHEN @outcome = '' OR (@outcome = 'unchanged' AND last_outcome IN ('refused', 'restart')) THEN last_error
			ELSE NULLIF(@error, '') END,
		last_outcome = CASE WHEN last_outcome = @not_served THEN NULLIF(@outcome, '')
			WHEN @outcome = '' OR (@outcome = 'unchanged' AND last_outcome IN ('refused', 'restart')) THEN last_outcome
			ELSE NULLIF(@outcome, '') END,
		served_revision_at = CASE WHEN served_revision IS DISTINCT FROM NULLIF(@served, '')
			THEN NOW() ELSE served_revision_at END,
		served_revision = NULLIF(@served, ''),
		capabilities = @capabilities,
		served_gen = CASE WHEN @composed THEN @gen ELSE served_gen END,
		composed_at = CASE WHEN @composed THEN NOW() ELSE composed_at END
	WHERE id = @id`, map[string]any{
		"version": version, "applied": appliedRevision, "outcome": lastOutcome, "error": lastError,
		"served": servedRevision, "capabilities": pq.StringArray(capabilities), "id": sidecarID,
		"not_served": SidecarOutcomeNotServed,
		"composed":   composedGen != nil, "gen": composedGen,
	}).Error
}

// SidecarOutcomeNotServed is the last_outcome RecordSidecarServeRefusal
// writes: the plane refused to serve this build, and last_error says why. It
// is the plane's verdict, apart from the outcomes a sidecar reports, so the
// next handshake the plane serves clears it (RecordSidecarHandshake).
const SidecarOutcomeNotServed = "not_served"

// RecordSidecarServeRefusal stores what a sidecar reported when the plane
// refused to serve it, and why, so the page shows the refusal with its reason
// and a later write knows this build is too old. It leaves last_seen_at
// alone: a sidecar that cannot run must not read as recently seen.
func RecordSidecarServeRefusal(db *gorm.DB, sidecarID, version string, capabilities []string, reason string) error {
	if capabilities == nil {
		capabilities = []string{}
	}
	return db.Exec(`
	UPDATE private.sidecars SET
		reported_version = NULLIF(?, ''),
		capabilities = ?,
		last_outcome = ?,
		last_error = NULLIF(?, '')
	WHERE id = ?`, version, pq.StringArray(capabilities), SidecarOutcomeNotServed, reason, sidecarID).Error
}
