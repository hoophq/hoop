package httputils

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	pkgerrors "github.com/pkg/errors"
	"go.uber.org/zap"
)

// AbortWithErr logs an internal error, attaches it to the gin context for
// middleware capture (e.g. Sentry), and aborts the request with the given
// HTTP status code and a user-facing JSON error message.
//
// The internal error and user message are combined into a single log entry,
// with the caller's location preserved in the log output.
func AbortWithErr(c *gin.Context, status int, err error, friendlyErrMsg string, friendlyErrMsgArgs ...any) {
	friendlyErrMsg = fmt.Sprintf(friendlyErrMsg, friendlyErrMsgArgs...)
	errMsg := fmt.Sprintf("%v, user-msg=%v", err, friendlyErrMsg)

	// preserve the caller when logging the error
	log.GetLogger().WithOptions(zap.AddCallerSkip(1)).Sugar().Error(errMsg)

	// append the error to the gin context, so that it can be captured by sentry middleware if needed
	c.Error(pkgerrors.WithStack(err))

	// respond with a friendly error message
	c.AbortWithStatusJSON(status, gin.H{"message": friendlyErrMsg})
}
