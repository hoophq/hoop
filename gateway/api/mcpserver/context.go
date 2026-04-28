package mcpserver

import (
	"context"

	"github.com/hoophq/hoop/gateway/storagev2"
)

type ctxKey struct{}
type tokenCtxKey struct{}

func withStorageContext(ctx context.Context, sc *storagev2.Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, sc)
}

func storageContextFrom(ctx context.Context) *storagev2.Context {
	sc, _ := ctx.Value(ctxKey{}).(*storagev2.Context)
	return sc
}

func withAccessToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenCtxKey{}, token)
}

func accessTokenFrom(ctx context.Context) string {
	t, _ := ctx.Value(tokenCtxKey{}).(string)
	return t
}
