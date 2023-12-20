package review

import (
	pgreview "github.com/runopsio/hoop/gateway/pgrest/review"
	pgsession "github.com/runopsio/hoop/gateway/pgrest/session"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Storage struct{}
)

func (s *Storage) FindApprovedJitReviews(ctx *user.Context, connID string) (*types.Review, error) {
	return pgreview.New().FetchJit(ctx, connID)
}

func (s *Storage) FindById(ctx *user.Context, id string) (*types.Review, error) {
	return pgreview.New().FetchOneByID(ctx, id)
}

func (s *Storage) PersistSessionAsReady(ctx *user.Context, sessionID string) error {
	return pgsession.New().UpdateStatus(ctx, sessionID, types.SessionStatusReady)
}

func (s *Storage) FindBySessionID(ctx *user.Context, sessionID string) (*types.Review, error) {
	return pgreview.New().FetchOneBySid(ctx, sessionID)
}

func (s *Storage) Persist(ctx *user.Context, review *types.Review) (int64, error) {
	return 0, pgreview.New().Upsert(review)
}
