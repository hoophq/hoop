package pgproxy

import (
	"encoding/binary"
	"fmt"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/pgtypes"
)

// https://www.postgresql.org/docs/current/protocol-flow.html#PROTOCOL-FLOW-CANCELING-REQUESTS
func (p *proxy) handleCancelRequest(pkt *pgtypes.Packet) error {
	frame := pkt.Frame()
	log.Infof("handling user cancel request for pid %v", binary.BigEndian.Uint32(frame[4:8]))
	if _, err := p.serverRW.Write(pkt.Encode()); err != nil {
		return fmt.Errorf("fail to write cancel request to server: %v", err)
	}
	_, pkt, err := pgtypes.DecodeTypedPacket(p.clientInitBuffer)
	if err != nil {
		return err
	}
	if _, err = p.clientW.Write(pkt.Encode()); err != nil {
		return fmt.Errorf("fail to write cancel request response to client: %v", err)
	}
	return nil
}
