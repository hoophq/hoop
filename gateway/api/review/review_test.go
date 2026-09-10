package reviewapi

import (
	"database/sql"
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

func newFakeReview(ownerID, status, typ string, groups []models.ReviewGroups, accessRequestRule *models.AccessRequestRule) *models.Review {
	var minApprovals *int
	var forceApprovalGroups []string
	var ruleName *string
	if accessRequestRule != nil {
		if accessRequestRule.AllGroupsMustApprove {
			// Mirrors createReview: the bar is the reviewer groups, not the
			// groups whose members need a review to begin with.
			minApprovals = ptr.Int(len(groups))
		} else {
			minApprovals = accessRequestRule.MinApprovals
		}

		forceApprovalGroups = accessRequestRule.ForceApprovalGroups
		ruleName = ptr.String("fake-rule")
	}

	return &models.Review{
		OwnerID:               ownerID,
		Status:                models.ReviewStatusType(status),
		Type:                  models.ReviewType(typ),
		ReviewGroups:          groups,
		MinApprovals:          minApprovals,
		ForceApprovalGroups:   forceApprovalGroups,
		AccessRequestRuleName: ruleName,
	}
}

type inputData struct {
	ctx    *storagev2.Context
	rev    *models.Review
	con    *models.Connection
	status models.ReviewStatusType
	force  bool
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
				}, &models.AccessRequestRule{
					MinApprovals:         ptr.Int(1),
					AllGroupsMustApprove: true,
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
				}, &models.AccessRequestRule{
					MinApprovals:         ptr.Int(1),
					AllGroupsMustApprove: false,
				}),
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
				}, &models.AccessRequestRule{
					MinApprovals: ptr.Int(1),
				}),
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
				}, nil),
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
				}, nil),
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
				}, nil),
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
				}, nil),
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
				}, nil),
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
				}, nil),
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
				}, nil),
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
				}, nil),
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
				}, nil),
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
				}, nil),
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
				}, nil),
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
				}, nil),
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
		{
			// EVL-250: the reviewer belongs to both reviewer groups, so one call
			// approves both while the rule only asks for one approval.
			name: "reviewer in more groups than the rule minimum settles the review",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"admin", "devops"}),
				rev: newFakeReview("user1", "PENDING", "jit", []models.ReviewGroups{
					{GroupName: "admin", Status: models.ReviewStatusPending},
					{GroupName: "devops", Status: models.ReviewStatusPending},
				}, &models.AccessRequestRule{
					MinApprovals:         ptr.Int(1),
					AllGroupsMustApprove: false,
				}),
				con:    &models.Connection{},
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, rev.ReviewGroups[0].Status)
				assert.Equal(t, models.ReviewStatusApproved, rev.ReviewGroups[1].Status)
				assert.Equal(t, models.ReviewStatusApproved, rev.Status)
			},
		},
		{
			// A review can carry a stored minimum of zero, which is what the
			// credentials path copies off an all groups rule. Taken literally it
			// would clear the bar before anyone approved, so the review must
			// still hold out for every reviewer group.
			name: "a minimum of zero still requires every reviewer group",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"admin"}),
				rev: newFakeReview("user1", "PENDING", "jit", []models.ReviewGroups{
					{GroupName: "admin", Status: models.ReviewStatusPending},
					{GroupName: "devops", Status: models.ReviewStatusPending},
				}, &models.AccessRequestRule{
					MinApprovals:         ptr.Int(0),
					AllGroupsMustApprove: false,
				}),
				con:    &models.Connection{},
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, rev.ReviewGroups[0].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.ReviewGroups[1].Status)
				assert.Equal(t, models.ReviewStatusPending, rev.Status)
			},
		},
		{
			// EVL-250, same overshoot through the legacy connection reviewers.
			name: "reviewer in more groups than the connection minimum settles the review",
			input: inputData{
				ctx: newFakeContext("user2", "user2@example.com", []string{"admin", "devops"}),
				rev: newFakeReview("user1", "PENDING", "jit", []models.ReviewGroups{
					{GroupName: "admin", Status: models.ReviewStatusPending},
					{GroupName: "devops", Status: models.ReviewStatusPending},
				}, nil),
				con: &models.Connection{
					MinReviewApprovals: ptr.Int(1),
				},
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, rev.ReviewGroups[0].Status)
				assert.Equal(t, models.ReviewStatusApproved, rev.ReviewGroups[1].Status)
				assert.Equal(t, models.ReviewStatusApproved, rev.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := doReview(tt.input.ctx, tt.input.rev, tt.input.con, tt.input.status, tt.input.force)
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
				}, nil),
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
				rev:    newFakeReview("user1", "PENDING", "onetime", nil, nil),
				con:    &models.Connection{},
				status: models.ReviewStatusApproved,
			},
			expectedError: ErrSelfApproval,
		},
		{
			name: "it must match wrong review state - not pending or approved",
			input: inputData{
				ctx:    newFakeContext("user2", "user2@example.com", []string{"issuing"}),
				rev:    newFakeReview("user1", string(models.ReviewStatusExecuted), "onetime", nil, nil),
				con:    &models.Connection{},
				status: models.ReviewStatusApproved,
			},
			expectedError: ErrWrongState,
		},
		{
			name: "revoke without approved status should fail",
			input: inputData{
				ctx:    newFakeContext("user1", "user1@example.com", []string{"issuing"}),
				rev:    newFakeReview("user1", "PENDING", "onetime", nil, nil),
				con:    &models.Connection{},
				status: models.ReviewStatusRevoked,
			},
			expectedError: ErrWrongState,
		},
		{
			name: "revoke JIT type should fail",
			input: inputData{
				ctx:    newFakeContext("user1", "user1@example.com", []string{"issuing"}),
				rev:    newFakeReview("user1", "APPROVED", "onetime", nil, nil),
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
				}, nil),
				con:    &models.Connection{},
				status: models.ReviewStatusApproved,
			},
			expectedError: ErrNotEligible,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rev, err := doReview(tt.input.ctx, tt.input.rev, tt.input.con, tt.input.status, tt.input.force)
			if tt.expectedError != nil {
				assert.EqualError(t, err, tt.expectedError.Error())
				return
			}

			assert.Nil(t, rev)
			assert.NoError(t, err)
		})
	}
}

// newFakeSidecarReview mirrors what the create path persists: a review bound to
// a listener, one pending group row per eligible role, min_approvals 1, and no
// connection, no rule, no access duration.
func newFakeSidecarReview(groups ...string) *models.Review {
	reviewGroups := make([]models.ReviewGroups, 0, len(groups))
	for _, name := range groups {
		reviewGroups = append(reviewGroups, models.ReviewGroups{
			GroupName: name,
			Status:    models.ReviewStatusPending,
		})
	}
	return &models.Review{
		ID:           "review-1",
		SessionID:    "session-1",
		OwnerID:      "sidecar-1",
		OwnerEmail:   "hoop@hoop.dev",
		Status:       models.ReviewStatusPending,
		Type:         models.ReviewTypeOneTime,
		MinApprovals: ptr.Int(1),
		SidecarID:    sql.NullString{String: "sidecar-1", Valid: true},
		ListenerName: sql.NullString{String: "appdb", Valid: true},
		ReviewGroups: reviewGroups,
	}
}

// A sidecar review runs the same decision path as every other review, with no
// connection. The policy is carried by its rows: one group per eligible role
// and min_approvals 1, so a single verdict settles it.
func TestDoReviewSidecar(t *testing.T) {
	tests := []struct {
		name          string
		input         inputData
		expectedError error
		validateFunc  func(t *testing.T, rev *models.Review)
	}{
		{
			name: "an admin settles it alone",
			input: inputData{
				ctx:    newFakeContext("u1", "admin@hoop.dev", []string{types.GroupAdmin}),
				rev:    newFakeSidecarReview(types.GroupAdmin, types.GroupApprover),
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, rev.Status)
			},
		},
		{
			name: "an approver settles it alone",
			input: inputData{
				ctx:    newFakeContext("u1", "approver@hoop.dev", []string{types.GroupApprover}),
				rev:    newFakeSidecarReview(types.GroupAdmin, types.GroupApprover),
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusApproved, rev.Status)
			},
		},
		{
			name: "only the reviewer's own group is stamped",
			input: inputData{
				ctx:    newFakeContext("u1", "approver@hoop.dev", []string{types.GroupApprover}),
				rev:    newFakeSidecarReview(types.GroupAdmin, types.GroupApprover),
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				for _, rg := range rev.ReviewGroups {
					if rg.GroupName == types.GroupApprover {
						assert.Equal(t, models.ReviewStatusApproved, rg.Status)
						assert.Equal(t, "approver@hoop.dev", *rg.OwnerEmail)
						continue
					}
					assert.Equal(t, models.ReviewStatusPending, rg.Status,
						"a group that did not review must stay pending")
				}
			},
		},
		{
			name: "a rejection settles it too",
			input: inputData{
				ctx:    newFakeContext("u1", "admin@hoop.dev", []string{types.GroupAdmin}),
				rev:    newFakeSidecarReview(types.GroupAdmin, types.GroupApprover),
				status: models.ReviewStatusRejected,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Equal(t, models.ReviewStatusRejected, rev.Status)
			},
		},
		{
			name: "no access window is stamped",
			input: inputData{
				ctx:    newFakeContext("u1", "admin@hoop.dev", []string{types.GroupAdmin}),
				rev:    newFakeSidecarReview(types.GroupAdmin),
				status: models.ReviewStatusApproved,
			},
			validateFunc: func(t *testing.T, rev *models.Review) {
				assert.Nil(t, rev.RevokedAt,
					"a sidecar review authorizes one named statement, not a window")
			},
		},
		{
			name: "force review does not dereference the missing connection",
			input: inputData{
				ctx:    newFakeContext("u1", "admin@hoop.dev", []string{types.GroupAdmin}),
				rev:    newFakeSidecarReview(types.GroupAdmin),
				status: models.ReviewStatusApproved,
				force:  true,
			},
			// No force-approval groups exist for a sidecar review, so this is
			// refused rather than granted. The point is that it refuses instead
			// of panicking: the review has no connection to read them from.
			expectedError: ErrNotEligible,
		},
		{
			name: "an unrelated group cannot approve",
			input: inputData{
				ctx:    newFakeContext("u1", "dev@hoop.dev", []string{"engineering"}),
				rev:    newFakeSidecarReview(types.GroupAdmin, types.GroupApprover),
				status: models.ReviewStatusApproved,
			},
			expectedError: ErrNotEligible,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// con is nil on purpose: a sidecar review has no connection, and
			// every connection read downstream must stay behind a guard.
			rev, err := doReview(tt.input.ctx, tt.input.rev, nil, tt.input.status, tt.input.force)
			if tt.expectedError != nil {
				assert.EqualError(t, err, tt.expectedError.Error())
				return
			}
			assert.NoError(t, err)
			tt.validateFunc(t, rev)
		})
	}
}
