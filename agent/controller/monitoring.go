package controller

import (
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/monitoring"
	pb "github.com/runopsio/hoop/common/proto"
)

func (a *Agent) startMonitoring(pkt *pb.Packet) {
	if len(pkt.Payload) == 0 {
		return
	}

	var conf monitoring.TransportConfig
	if err := pb.GobDecodeInto(pkt.Payload, &conf); err != nil {
		log.Infof("profiler - failed decoding profiler.Config, err=%v", err)
		return
	}

	if _, err := monitoring.StartSentry(nil, conf.Sentry); err != nil {
		log.Infof("sentry - failed to initialize, err=%v", err)
		return
	}
}
