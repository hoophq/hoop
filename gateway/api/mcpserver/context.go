package mcpserver

import (
	"context"

	"github.com/hoophq/hoop/gateway/storagev2"
)

type ctxKey struct{}

func withStorageContext(ctx context.Context, sc *storagev2.Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, sc)
}

func storageContextFrom(ctx context.Context) *storagev2.Context {
	sc, _ := ctx.Value(ctxKey{}).(*storagev2.Context)
	return sc
}
