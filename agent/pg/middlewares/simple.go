package middlewares

import (
	"github.com/runopsio/hoop/common/log"

	"github.com/runopsio/hoop/agent/pg"
)

// HexDumpPacket is a simple middleware that dumps packet to stdout
func HexDumpPacket(next pg.NextFn, pkt *pg.Packet, w pg.ResponseWriter) {
	log.Println("packet hexdump")
	pkt.Dump()
	next()
}
