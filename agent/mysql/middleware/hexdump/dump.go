package middleware

import (
	"io"

	"github.com/runopsio/hoop/agent/mysql"
	"github.com/runopsio/hoop/agent/mysql/types"
	"github.com/runopsio/hoop/common/log"
)

// HexDumpPacket is a simple middleware that dumps packets to stdout when the
// logger is in debug level.
func HexDumpPacket(next mysql.NextFn, pkt *types.Packet, w io.WriteCloser) {
	log.Debugf("source=%v, seq=%v, length=%v", pkt.Source, pkt.Seq(), len(pkt.Frame))
	pkt.Dump()
	next()
}
