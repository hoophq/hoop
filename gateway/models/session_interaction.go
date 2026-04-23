package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SessionInteraction represents a single command execution within a long-lived machine session.
type SessionInteraction struct {
	ID           string     `gorm:"column:id"`
	SessionID    string     `gorm:"column:session_id"`
	OrgID        string     `gorm:"column:org_id"`
	Sequence     int        `gorm:"column:sequence"`
	BlobInputID  *string    `gorm:"column:blob_input_id"`
	BlobStreamID *string    `gorm:"column:blob_stream_id"`
	ExitCode     *int       `gorm:"column:exit_code"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	EndedAt      *time.Time `gorm:"column:ended_at"`
}

// CreateInteractionWithBlobs inserts blobs and a session_interactions row in a single transaction.
func CreateInteractionWithBlobs(db *gorm.DB, interaction SessionInteraction, blobInput json.RawMessage, blobStream json.RawMessage, blobFormat *string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// create input blob with deterministic ID
		if blobInput != nil {
			blobInputID := uuid.NewSHA1(uuid.NameSpaceURL, fmt.Appendf(nil, "blobinput:interaction:%s", interaction.ID)).String()
			interaction.BlobInputID = &blobInputID

			inputBlob := Blob{
				ID:         blobInputID,
				OrgID:      interaction.OrgID,
				BlobStream: blobInput,
				Type:       "session-input",
			}
			res := tx.Table("private.blobs").
				Where("org_id = ? AND id = ?", interaction.OrgID, blobInputID).
				Updates(inputBlob)
			if res.Error == nil && res.RowsAffected == 0 {
				res.Error = tx.Table("private.blobs").Create(inputBlob).Error
			}
			if res.Error != nil {
				return fmt.Errorf("failed creating interaction input blob: %v", res.Error)
			}
		}

		// create stream blob with deterministic ID
		blobStreamID := uuid.NewSHA1(uuid.NameSpaceURL, fmt.Appendf(nil, "blobstream:interaction:%s", interaction.ID)).String()
		interaction.BlobStreamID = &blobStreamID

		streamBlob := Blob{
			ID:         blobStreamID,
			OrgID:      interaction.OrgID,
			BlobStream: blobStream,
			Type:       "session-stream",
			BlobFormat: blobFormat,
		}
		res := tx.Table("private.blobs").
			Where("org_id = ? AND id = ?", interaction.OrgID, blobStreamID).
			Updates(streamBlob)
		if res.Error == nil && res.RowsAffected == 0 {
			res.Error = tx.Table("private.blobs").Create(streamBlob).Error
		}
		if res.Error != nil {
			return fmt.Errorf("failed creating interaction stream blob: %v", res.Error)
		}

		// insert interaction row
		return tx.Table("private.session_interactions").Create(&interaction).Error
	})
}

// ListSessionInteractions returns interactions for a session with sequence > afterSequence, ordered by sequence ASC.
func ListSessionInteractions(db *gorm.DB, orgID, sessionID string, afterSequence, limit int) ([]SessionInteraction, error) {
	var interactions []SessionInteraction
	err := db.Table("private.session_interactions").
		Where("org_id = ? AND session_id = ? AND sequence > ?", orgID, sessionID, afterSequence).
		Order("sequence ASC").
		Limit(limit).
		Find(&interactions).Error
	return interactions, err
}

// GetInteractionBlobInput fetches the input blob for an interaction.
func GetInteractionBlobInput(db *gorm.DB, orgID string, blobInputID string) (BlobInputType, error) {
	var blob Blob
	err := db.Table("private.blobs").
		Where("org_id = ? AND id = ? AND type = 'session-input'", orgID, blobInputID).
		First(&blob).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var result []string
	if err := json.Unmarshal(blob.BlobStream, &result); err != nil {
		return "", fmt.Errorf("failed decoding interaction blob input: %v", err)
	}
	if len(result) == 0 {
		return "", nil
	}
	return BlobInputType(result[0]), nil
}

// GetInteractionBlobStream fetches the stream blob for an interaction.
func GetInteractionBlobStream(db *gorm.DB, orgID string, blobStreamID string) (*Blob, error) {
	var blob Blob
	err := db.Table("private.blobs").
		Where("org_id = ? AND id = ?", orgID, blobStreamID).
		First(&blob).Error

	return &blob, err
}
