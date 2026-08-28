// Package apierr is the error shape for every response under /api: one
// shape, {"message": "..."}, because that is the field the frontend reads.
// Probe endpoints outside /api answer an orchestrator and do not use it.
//
// Its own package so feature packages can import it without importing the
// parent api package, which imports them.
package apierr

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Body is the error response.
type Body struct {
	Message string `json:"message"`
}

// JSON writes status with message, for client errors.
func JSON(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, Body{Message: message})
}

// Internal records err on the context (logged by the request logger) and
// writes a 500 carrying only message: raw errors leak table names, paths and
// connection strings to the client.
func Internal(c *gin.Context, err error, message string) {
	_ = c.Error(err)
	c.AbortWithStatusJSON(http.StatusInternalServerError, Body{Message: message})
}

// NotImplemented answers a route that is wired but not built. 501, never an
// empty 200, so a missing component fails loudly. what names the missing
// behaviour, not the route. Delete the call, not the route, when the
// component lands.
func NotImplemented(c *gin.Context, what string) {
	c.AbortWithStatusJSON(http.StatusNotImplemented, Body{
		Message: NotImplementedMessage(what),
	})
}

// NotImplementedMessage is the body NotImplemented writes. Exported so tests
// assert the whole message: several descriptions are prefixes of each other,
// so a substring check could pass on a miswired route.
func NotImplementedMessage(what string) string {
	return what + " is not implemented yet"
}
