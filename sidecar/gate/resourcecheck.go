package gate

import (
	"context"
	"errors"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

type resourceCheck struct {
	check inspect.ResourceCheck
	side  SidePath
	// skip: another statement of the same client chunk stops the check
	// (inspect.ChunkStopper). A check runs before every statement of the
	// chunk.
	skip bool
}

type chunkSkipsKey struct{}

func chunkSkips(ctx context.Context) bool {
	skip, _ := ctx.Value(chunkSkipsKey{}).(bool)
	return skip
}

func (r resourceCheck) Scope() string { return r.check.Scope() }

func (r resourceCheck) Skips(stmt inspect.Statement) bool { return r.skip || r.check.Skips(stmt) }

func (r resourceCheck) Check(ctx context.Context, stmt inspect.Statement, want inspect.CheckWant) (inspect.CheckResult, error) {
	if !r.check.Ready() {
		return inspect.CheckResult{}, policy.ErrCheckNotReady
	}
	req, resp, err := r.check.Request(stmt, want)
	if err != nil {
		return inspect.CheckResult{}, err
	}
	err = r.side.Exchange(ctx, req, resp, r.check.CancelRequest())
	if err != nil && !errors.Is(err, policy.ErrCheckCancelled) {
		return inspect.CheckResult{}, err
	}
	return resp.Result(), err
}
