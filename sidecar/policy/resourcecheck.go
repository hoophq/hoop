package policy

import (
	"context"
	"errors"

	"github.com/hoophq/hoop/sidecar/inspect"
)

type ResourceChecker interface {
	Scope() string

	Skips(stmt inspect.Statement) bool

	// Check returns ErrCheckNotReady, having sent nothing, when the
	// connection cannot take a check.
	Check(ctx context.Context, stmt inspect.Statement, want inspect.CheckWant) (inspect.CheckResult, error)
}

var ErrCheckNotReady = errors.New("the connection cannot take a resource check now")

// UsesResourceCheck also looks inside Chain and Observe.
func UsesResourceCheck(e Evaluator) bool {
	switch v := e.(type) {
	case Chain:
		for _, inner := range v {
			if UsesResourceCheck(inner) {
				return true
			}
		}
	case Observe:
		return UsesResourceCheck(v.Evaluator)
	case interface{ UsesResourceCheck() bool }:
		return v.UsesResourceCheck()
	}
	return false
}
