package proxy

import "io"

type Closer interface {
	io.Closer
	CloseTCPConnection(connectionID string)
}
