package sessionstorage

import (
	"github.com/google/uuid"
	pgreview "github.com/hoophq/hoop/gateway/pgrest/review"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

func PutReview(ctx *storagev2.Context, review *types.Review) (int64, error) {
	if review.Id == "" {
		review.Id = uuid.NewString()
	}
	return 0, pgreview.New().PatchStatus(ctx, review.Id, review.Status)
}
