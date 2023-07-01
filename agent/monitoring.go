package agent

import (
	"github.com/pyroscope-io/client/pyroscope"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/monitoring"
	pb "github.com/runopsio/hoop/common/proto"
)

func (a *Agent) startMonitoring(pkt *pb.Packet) {
	if len(pkt.Payload) == 0 {
		return
	}
	// pyroscope setup
	if prof, _ := a.connStore.Get(profilerInstanceKey).(*pyroscope.Profiler); prof != nil {
		log.Infof("profiler - found a profiler instance, stopping it ...")
		_ = prof.Stop()
	}

	var conf monitoring.TransportConfig
	if err := pb.GobDecodeInto(pkt.Payload, &conf); err != nil {
		log.Infof("profiler - failed decoding profiler.Config, err=%v", err)
		return
	}

	prof, err := monitoring.StartProfiler("agent", conf.Profiler)
	if err != nil {
		log.Infof("profiler - failed starting, err=%v", err)
		return
	}
	if prof != nil {
		log.Infof("profiler - started profiler, environment=%v, serveraddress=%v",
			conf.Profiler.Environment, conf.Profiler.PyroscopeServerAddress)
		a.connStore.Set(profilerInstanceKey, prof)
	}

	if _, err := monitoring.StartSentry(nil, conf.Sentry); err != nil {
		log.Infof("sentry - failed to initialize, err=%v", err)
		return
	}
}
