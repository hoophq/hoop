// Package apierr is the error shape for every response under /api.
//
// One shape, {"message": "..."}, and nothing else. That is what
// controlplane/frontend already reads: every form in it does
// error.response.data.message and falls back to a generic string when the
// field is absent. A handler that invents a different shape produces a UI
// saying "Something went wrong" while the real reason sits in the response
// nobody renders.
//
// The probe endpoints outside /api do not use this. They answer an
// orchestrator, not the frontend, and their bodies are for a human reading
// kubectl output.
//
// It is its own package so the feature packages can use it without importing
// httpapi, which imports them.
package apierr

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Body is the error response.
type Body struct {
	Message string `json:"message"`
}

// JSON writes status with message. Use it for client errors, where the
// message is meant to be read by whoever made the request.
func JSON(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, Body{Message: message})
}

// Internal records err on the context and writes a 500 carrying message.
//
// The split matters: err reaches the log through the request logger, which
// reads c.Errors, while message goes to the client. Returning a raw error to
// an HTTP client leaks table names, file paths and occasionally connection
// strings, and a 500 is by definition a case where the client cannot act on
// the detail anyway.
//
// The error goes on the context rather than to a logger held here, so the
// request line and the failure are one record instead of two that a reader
// has to correlate by timestamp.
func Internal(c *gin.Context, err error, message string) {
	_ = c.Error(err)
	c.AbortWithStatusJSON(http.StatusInternalServerError, Body{Message: message})
}

// NotImplemented answers a route that is wired but not built, naming the
// ticket that owns it.
//
// This is the scaffold's contract with the four component workstreams. A
// route that exists must either work or say plainly that it does not: the
// alternative, an empty 200 with an empty list, reads to a caller as "there
// is nothing here" and to an operator as "the fleet is empty". 501 with the
// owner in the message costs one line and removes a whole class of misleading
// state, which is what the root CLAUDE.md means by an incomplete path failing
// loudly.
//
// Delete the call, not the route, when the component lands.
func NotImplemented(c *gin.Context, ticket, what string) {
	c.AbortWithStatusJSON(http.StatusNotImplemented, Body{
		Message: what + " is not implemented yet, owned by " + ticket,
	})
}
