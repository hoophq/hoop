package proto

import (
	"google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

var ErrAgentOffline = status.Errorf(codes.FailedPrecondition, "agent is offline")
