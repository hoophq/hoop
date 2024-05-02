package mongotypes

const (
	// Wraps other opcodes using compression
	OpCompressed uint32 = 2012
	// Send a message using the standard format.
	// Used for both client requests and database replies.
	OpMsgType uint32 = 2013

	// Query a collection.
	// Deprecated in MongoDB 5.0. Removed in MongoDB 5.1.
	OpQueryType uint32 = 2004
	// Reply to a client request. responseTo is set.
	// Deprecated in MongoDB 5.0. Removed in MongoDB 5.1.
	OpReplyType uint32 = 1
)
