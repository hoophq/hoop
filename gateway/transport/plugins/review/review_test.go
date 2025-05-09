package review

import (
	"fmt"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
)

func newTime(hour, minute int) *time.Time {
	t := time.Date(2024, time.September, 9, hour, minute, 0, 0, time.UTC)
	return &t
}

func TestValidateJit(t *testing.T) {
	for _, tt := range []struct {
		msg string
		jit *models.ReviewJit
		now *time.Time
		err error
	}{
		{
			msg: "it should validate without any error when jit is not expired",
			now: newTime(10, 19),
			jit: &models.ReviewJit{
				RevokedAt: newTime(10, 20),
			},
		},
		{
			msg: "it should validate with error if jit is expired",
			now: newTime(10, 21),
			jit: &models.ReviewJit{
				RevokedAt: newTime(10, 20),
			},
			err: errJitExpired,
		},
		{
			msg: "it should validate with error if revoked at is nil",
			now: newTime(10, 21),
			jit: &models.ReviewJit{
				RevokedAt: nil,
			},
			err: fmt.Errorf("internal error, found inconsistent jit record"),
		},
		{
			msg: "it should validate with error if revoked at is zero",
			now: newTime(10, 21),
			jit: &models.ReviewJit{
				RevokedAt: func() *time.Time { t := time.Time{}; return &t }(),
			},
			err: fmt.Errorf("internal error, found inconsistent jit record"),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			err := validateJit(tt.jit, *tt.now)
			if tt.err != nil {
				assert.EqualError(t, err, tt.err.Error())
				return
			}
			assert.Nil(t, err)
		})
	}
}
