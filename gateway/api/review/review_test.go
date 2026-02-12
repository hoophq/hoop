package reviewapi

import (
	"testing"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
)

// Helper methods provided by the user
func newFakeContext(id, email string, grp []string) *storagev2.Context {
	return &storagev2.Context{
		APIContext: &types.APIContext{
			UserID:     id,
			UserEmail:  email,
			UserGroups: grp,
		},
	}
}

func newFakeReview(ownerID, status, typ string, groups []models.ReviewGroups) *models.Review {
	return &models.Review{
		OwnerID:      ownerID,
		Status:       models.ReviewStatusType(status),
		Type:         models.ReviewType(typ),
		ReviewGroups: groups,
	}
}

type inputData struct {
	ctx        *storagev2.Context
	rev        *models.Review
	con        *models.Connection
	accessRule *models.AccessRequestRule
	status     models.ReviewStatusType
	force      bool
}

func TestDoReview(t *testing.T) {
	tests := []struct {
		name         string
		input        inputData
		validateFunc func(t *testing.T, rev *models.Review)
	}{
		{
			name: "partial approve with access request rule",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"issuing"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "issuing", Status: models.ReviewStatusPending},
					{GroupName: "banking", Status: models.ReviewStatusPending},
					{GroupName: "engineering", Status: models.ReviewStatusPending},
				}),
				accessRule: &models.AccessRequestRule{
					MinApprovals:         ptr.Int(1),
					AllGroupsMustApprove: true,
				},
				con: &models.Connection{
					MinReviewApprovals: ptr.Int(1),
				},
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, rev.ReviewGroups[0].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.ReviewGroups[1].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.ReviewGroups[2].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.Status)
			},
		},
		{
			name: "approve with minimal groups from access request rule",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"issuing"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "issuing", Status: models.ReviewStatusPending},
					{GroupName: "banking", Status: models.ReviewStatusPending},
					{GroupName: "engineering", Status: models.ReviewStatusPending},
				}),
				accessRule: &models.AccessRequestRule{
					MinApprovals:         ptr.Int(1),
					AllGroupsMustApprove: false,
				},
				con: &models.Connection{
					MinReviewApprovals: ptr.Int(3),
				},
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, rev.ReviewGroups[0].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.ReviewGroups[1].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.ReviewGroups[2].Status)
				assert.Equal(t, models.ReviewStatusApproved, rev.Status)
			},
		},
		{
			name: "approve with minimal groups from access request rule",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"issuing"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "issuing", Status: models.ReviewStatusPending},
					{GroupName: "banking", Status: models.ReviewStatusPending},
					{GroupName: "engineering", Status: models.ReviewStatusPending},
				}),
				accessRule: &models.AccessRequestRule{
					MinApprovals: ptr.Int(1),
				},
				con: &models.Connection{
					MinReviewApprovals: ptr.Int(3),
				},
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, rev.ReviewGroups[0].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.ReviewGroups[1].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.ReviewGroups[2].Status)
				assert.Equal(t, models.ReviewStatusApproved, rev.Status)
			},
		},
		{
			name: "partial approve with minimal groups",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"issuing"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "issuing", Status: models.ReviewStatusPending},
					{GroupName: "banking", Status: models.ReviewStatusPending},
					{GroupName: "engineering", Status: models.ReviewStatusPending},
				}),
				con: &models.Connection{
					MinReviewApprovals: ptr.Int(2),
				},
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, rev.ReviewGroups[0].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.ReviewGroups[1].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.ReviewGroups[2].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.Status)
			},
		},
		{
			name: "approve with minimal of one group",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"issuing"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "issuing", Status: models.ReviewStatusPending},
					{GroupName: "banking", Status: models.ReviewStatusPending},
					{GroupName: "engineering", Status: models.ReviewStatusPending},
				}),
				con: &models.Connection{
					MinReviewApprovals: ptr.Int(1),
				},
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, rev.ReviewGroups[0].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.ReviewGroups[1].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.ReviewGroups[2].Status)
				assert.Equal(t, models.ReviewStatusApproved, rev.Status)
			},
		},
		{
			name: "successful force approve by a eligible reviewer - multiple groups",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"issuing"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "sre-team", Status: models.ReviewStatusPending},
					{GroupName: "engineering", Status: models.ReviewStatusPending},
					{GroupName: "management", Status: models.ReviewStatusPending},
				}),
				con: &models.Connection{
					ForceApproveGroups: []string{"issuing"},
				},
				status: models.ReviewStatusApproved,
				force:  true,
			},
			validateFunc: func(t *testing.T, got *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, got.ReviewGroups[3].Status)
				assert.Equal(t, models.ReviewStatusApproved, got.Status)
				assert.NotNil(t, got.ReviewGroups[3].ReviewedAt)
			},
		},
		{
			name: "successful force approve by a eligible reviewer - multiple groups",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"engineering"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "sre-team", Status: models.ReviewStatusPending},
					{GroupName: "engineering", Status: models.ReviewStatusPending},
					{GroupName: "management", Status: models.ReviewStatusPending},
				}),
				con: &models.Connection{
					ForceApproveGroups: []string{"engineering"},
				},
				status: models.ReviewStatusApproved,
				force:  true,
			},
			validateFunc: func(t *testing.T, got *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, got.ReviewGroups[1].Status)
				assert.Equal(t, models.ReviewStatusApproved, got.Status)
				assert.NotNil(t, got.ReviewGroups[1].ReviewedAt)
			},
		},
		{
			name: "successful force approve by a eligible reviewer",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"engineering"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "engineering", Status: models.ReviewStatusPending},
				}),
				con: &models.Connection{
					ForceApproveGroups: []string{"engineering"},
				},
				status: models.ReviewStatusApproved,
				force:  true,
			},
			validateFunc: func(t *testing.T, got *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, got.ReviewGroups[0].Status)
				assert.Equal(t, models.ReviewStatusApproved, got.Status)
				assert.NotNil(t, got.ReviewGroups[0].ReviewedAt)
			},
		},
		{
			name: "successful force approve by a non-eligible reviewer",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"engineering"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "issuing", Status: models.ReviewStatusPending},
				}),
				con: &models.Connection{
					ForceApproveGroups: []string{"engineering"},
				},
				status: models.ReviewStatusApproved,
				force:  true,
			},
			validateFunc: func(t *testing.T, got *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, got.ReviewGroups[1].Status)
				assert.Equal(t, models.ReviewStatusApproved, got.Status)
				assert.NotNil(t, got.ReviewGroups[1].ReviewedAt)
			},
		},
		{
			name: "successful approval by eligible reviewer",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"issuing"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "issuing", Status: models.ReviewStatusPending},
				}),
				con:    &models.Connection{},
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, got *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, got.ReviewGroups[0].Status)
				assert.Equal(t, models.ReviewStatusApproved, got.Status)
				assert.NotNil(t, got.ReviewGroups[0].ReviewedAt)
			},
		},
		{
			name: "successful rejection by eligible reviewer",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"issuing"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "issuing", Status: models.ReviewStatusPending},
				}),
				con:    &models.Connection{},
				status: models.ReviewStatusRejected,
			},
			validateFunc: func(t *testing.T, got *models.Review) {
				assert.Equal(t, models.ReviewStatusRejected, got.ReviewGroups[0].Status)
				assert.Equal(t, models.ReviewStatusRejected, got.Status)
				assert.NotNil(t, got.ReviewGroups[0].ReviewedAt)
			},
		},
		{
			name: "partial approval - not all groups approved yet",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"issuing"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "issuing", Status: models.ReviewStatusPending},
					{GroupName: "banking", Status: models.ReviewStatusPending},
				}),
				con:    &models.Connection{},
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, rev.ReviewGroups[0].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.ReviewGroups[1].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.Status)
				assert.Nil(t, rev.RevokedAt) // should not be set yet
			},
		},
		{
			name: "admin can deny review even without being eligible reviewer",
			input: inputData{
				ctx: newFakeContext("admin", "admin@example.com", []string{"admin"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "issuing", Status: models.ReviewStatusPending},
				}),
				con:    &models.Connection{},
				status: models.ReviewStatusRejected,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusRejected, rev.Status)
				// Admin should create a new review group entry
				assert.Len(t, rev.ReviewGroups, 2)
				assert.Equal(t, models.ReviewStatusRejected, rev.ReviewGroups[1].Status)
				assert.Equal(t, "admin", *rev.ReviewGroups[1].OwnerID)
			},
		},
		{
			name: "resource owner can deny their own review",
			input: inputData{
				ctx: newFakeContext("user1", "user1@example.com", []string{"banking"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "issuing", Status: models.ReviewStatusPending},
				}),
				con:    &models.Connection{},
				status: models.ReviewStatusRejected,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusRejected, rev.Status)
				// Resource owner should create a new review group entry
				assert.Len(t, rev.ReviewGroups, 2)
				assert.Equal(t, models.ReviewStatusRejected, rev.ReviewGroups[1].Status)
			},
		},
		{
			name: "successful revoke of approved review",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"issuing"}),
				rev: newFakeReview("user1", "APPROVED", "jit", []models.ReviewGroups{
					{GroupName: "issuing", Status: models.ReviewStatusApproved},
				}),
				con:    &models.Connection{},
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, rev.Status)
				assert.Equal(t, models.ReviewStatusApproved, rev.ReviewGroups[0].Status)
			},
		},
		{
			name: "complete approval with access duration",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"issuing"}),
				rev: &models.Review{
					OwnerID:           "user1",
					Status:            models.ReviewStatusPending,
					Type:              "onetime",
					AccessDurationSec: 3600, // 1 hour
					ReviewGroups: []models.ReviewGroups{
						{GroupName: "issuing", Status: models.ReviewStatusPending},
					},
				},
				con:    &models.Connection{},
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, rev.Status)
				assert.NotNil(t, rev.RevokedAt)
				// Check that RevokedAt is approximately 1 hour from now
				expectedRevoke := time.Now().UTC().Add(time.Hour)
				assert.WithinDuration(t, expectedRevoke, *rev.RevokedAt, time.Minute)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := doReview(tt.input.ctx, tt.input.rev, tt.input.con, tt.input.accessRule, tt.input.status, tt.input.force)
			assert.NoError(t, err)
			tt.validateFunc(t, got)
			// assert.Equal(t, tt.expectedReview, tt.input.rev)
		})
	}
}

func TestErrDoReview(t *testing.T) {
	tests := []struct {
		name          string
		input         inputData
		expectedError error
	}{
		{
			name: "force approve by a not listed force approve group should fail",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"issuing"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "sre-team", Status: models.ReviewStatusPending},
					{GroupName: "engineering", Status: models.ReviewStatusPending},
					{GroupName: "management", Status: models.ReviewStatusPending},
				}),
				con: &models.Connection{
					ForceApproveGroups: []string{"admin"},
				},
				status: models.ReviewStatusApproved,
				force:  true,
			},
			expectedError: ErrNotEligible,
		},
		{
			name: "it must match unknown status error",
			input: inputData{
				ctx:    nil,
				rev:    nil,
				con:    &models.Connection{},
				status: models.ReviewStatusType("deny"),
			},
			expectedError: ErrUnknownStatus,
		},
		{
			name: "self approval should fail",
			input: inputData{
				ctx:    newFakeContext("user1", "user1@example.com", []string{"issuing"}),
				rev:    newFakeReview("user1", "PENDING", "onetime", nil),
				con:    &models.Connection{},
				status: models.ReviewStatusApproved,
			},
			expectedError: ErrSelfApproval,
		},
		{
			name: "it must match wrong review state - not pending or approved",
			input: inputData{
				ctx:    newFakeContext("user2", "user2@example.com", []string{"issuing"}),
				rev:    newFakeReview("user1", string(models.ReviewStatusExecuted), "onetime", nil),
				con:    &models.Connection{},
				status: models.ReviewStatusApproved,
			},
			expectedError: ErrWrongState,
		},
		{
			name: "revoke without approved status should fail",
			input: inputData{
				ctx:    newFakeContext("user1", "user1@example.com", []string{"issuing"}),
				rev:    newFakeReview("user1", "PENDING", "onetime", nil),
				con:    &models.Connection{},
				status: models.ReviewStatusRevoked,
			},
			expectedError: ErrWrongState,
		},
		{
			name: "revoke JIT type should fail",
			input: inputData{
				ctx:    newFakeContext("user1", "user1@example.com", []string{"issuing"}),
				rev:    newFakeReview("user1", "APPROVED", "onetime", nil),
				con:    &models.Connection{},
				status: models.ReviewStatusRevoked,
			},
			expectedError: ErrNotFound,
		},
		{
			name: "non-eligible reviewer without admin or owner privileges",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"banking"}),
				rev: newFakeReview("user1", "PENDING", "onetime", []models.ReviewGroups{
					{GroupName: "issuing", Status: "PENDING"},
				}),
				con:    &models.Connection{},
				status: models.ReviewStatusApproved,
			},
			expectedError: ErrNotEligible,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rev, err := doReview(tt.input.ctx, tt.input.rev, tt.input.con, tt.input.accessRule, tt.input.status, tt.input.force)
			if tt.expectedError != nil {
				assert.EqualError(t, err, tt.expectedError.Error())
				return
			}

			assert.Nil(t, rev)
			assert.NoError(t, err)
		})
	}
}
