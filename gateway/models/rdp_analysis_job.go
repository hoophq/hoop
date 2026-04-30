package models

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// RDPAnalysisJob represents an async job for PII analysis of an RDP session recording.
type RDPAnalysisJob struct {
	ID         string     `gorm:"column:id;primaryKey"`
	OrgID      string     `gorm:"column:org_id"`
	SessionID  string     `gorm:"column:session_id"`
	Status     string     `gorm:"column:status"`
	Priority   int        `gorm:"column:priority"`
	Attempt    int        `gorm:"column:attempt"`
	LastError  *string    `gorm:"column:last_error"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
	StartedAt  *time.Time `gorm:"column:started_at"`
	FinishedAt *time.Time `gorm:"column:finished_at"`
}

// RDP analysis job status constants
const (
	RDPJobStatusPending = "pending"
	RDPJobStatusRunning = "running"
	RDPJobStatusDone    = "done"
	RDPJobStatusFailed  = "failed"
)

// maxJobAttempts is the number of times a job will be retried before giving up.
const maxJobAttempts = 3

// CreateRDPAnalysisJob inserts a new pending analysis job for the given session.
func CreateRDPAnalysisJob(orgID, sessionID string) error {
	return DB.Table("private.rdp_analysis_jobs").
		Omit("id").
		Create(&RDPAnalysisJob{
			OrgID:     orgID,
			SessionID: sessionID,
			Status:    RDPJobStatusPending,
		}).Error
}

// ClaimRDPAnalysisJob atomically claims the next available job using SELECT FOR UPDATE SKIP LOCKED.
// Returns the claimed job, or nil if no jobs are available.
func ClaimRDPAnalysisJob(db *gorm.DB) (*RDPAnalysisJob, error) {
	var job RDPAnalysisJob
	err := db.Transaction(func(tx *gorm.DB) error {
		// Use a raw SQL query for the atomic claim with SKIP LOCKED
		result := tx.Raw(`
			UPDATE private.rdp_analysis_jobs
			SET status = ?, started_at = now(), attempt = attempt + 1
			WHERE id = (
				SELECT id FROM private.rdp_analysis_jobs
				WHERE status IN (?, ?) AND attempt < ?
				ORDER BY priority DESC, created_at ASC
				LIMIT 1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING *`,
			RDPJobStatusRunning,
			RDPJobStatusPending, RDPJobStatusFailed,
			maxJobAttempts,
		).Scan(&job)

		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})

	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &job, err
}

// CompleteRDPAnalysisJob marks a job as done.
func CompleteRDPAnalysisJob(db *gorm.DB, jobID string) error {
	return db.Table("private.rdp_analysis_jobs").
		Where("id = ?", jobID).
		Updates(map[string]any{
			"status":      RDPJobStatusDone,
			"finished_at": gorm.Expr("now()"),
		}).Error
}

// FailRDPAnalysisJob marks a job as failed with an error message.
// If the job has exceeded max attempts, it stays as 'failed' permanently.
func FailRDPAnalysisJob(db *gorm.DB, jobID string, errMsg string) error {
	return db.Table("private.rdp_analysis_jobs").
		Where("id = ?", jobID).
		Updates(map[string]any{
			"status":      RDPJobStatusFailed,
			"last_error":  errMsg,
			"finished_at": gorm.Expr("now()"),
		}).Error
}

// GetRDPAnalysisJobBySessionID returns the analysis job for a session, if any.
func GetRDPAnalysisJobBySessionID(sessionID string) (*RDPAnalysisJob, error) {
	var job RDPAnalysisJob
	err := DB.Table("private.rdp_analysis_jobs").
		Where("session_id = ?", sessionID).
		Order("created_at DESC").
		First(&job).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &job, err
}

// UpdateSessionRDPAnalysisStatus updates the rdp_analysis_status key in sessions.metrics JSONB.
func UpdateSessionRDPAnalysisStatus(orgID, sessionID, status string) error {
	statusJSON, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return DB.Table("private.sessions AS s").
		Where("s.id = ? AND s.org_id = ?", sessionID, orgID).
		Update("metrics", gorm.Expr(`
			jsonb_set(
				COALESCE(s.metrics, '{}'::jsonb),
				'{rdp_analysis_status}',
				?::jsonb,
				true
			)
		`, string(statusJSON))).Error
}

// ResetOrphanedRDPAnalysisJobs resets jobs left in 'running' state back to 'pending'.
// Called at gateway startup to recover from crashes/restarts that left jobs
// stuck in 'running' with no worker processing them.
// Returns the number of jobs that were reset.
func ResetOrphanedRDPAnalysisJobs(db *gorm.DB) (int64, error) {
	result := db.Exec(`
		UPDATE private.rdp_analysis_jobs
		SET status = ?, started_at = NULL
		WHERE status = ?
	`, RDPJobStatusPending, RDPJobStatusRunning)
	return result.RowsAffected, result.Error
}
