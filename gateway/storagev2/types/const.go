package types

type ClientStatusType string
type UserStatusType string

const (
	// ClientStatusReady indicates the grpc client is ready to
	// subscribe to a new connection
	ClientStatusReady ClientStatusType = "ready"
	// ClientStatusConnected indicates the client has opened a new session
	ClientStatusConnected ClientStatusType = "connected"
	// ClientStatusDisconnected indicates the grpc client has disconnected
	ClientStatusDisconnected ClientStatusType = "disconnected"

	UserStatusActive    UserStatusType = "active"
	UserStatusReviewing UserStatusType = "reviewing"
	UserStatusInactive  UserStatusType = "inactive"
)

type ReviewStatus string

const (
	ReviewStatusPending    ReviewStatus = "PENDING"
	ReviewStatusApproved   ReviewStatus = "APPROVED"
	ReviewStatusRejected   ReviewStatus = "REJECTED"
	ReviewStatusRevoked    ReviewStatus = "REVOKED"
	ReviewStatusProcessing ReviewStatus = "PROCESSING"
	ReviewStatusExecuted   ReviewStatus = "EXECUTED"
	ReviewStatusUnknown    ReviewStatus = "UNKNOWN"
)
