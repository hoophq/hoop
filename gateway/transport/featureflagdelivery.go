package transport

import (
	"encoding/json"
	"sync"

	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbgateway "github.com/hoophq/hoop/common/proto/gateway"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
)

// FeatureFlagStream is an agent stream that takes feature flag updates.
type FeatureFlagStream interface {
	Send(*pb.Packet) error
	GetOrgID() string
	AgentName() string
}

// FeatureFlagDelivery sends an org's flags to its agents without waiting on
// them. A gRPC send blocks while the agent does not read; a caller (the API,
// the refresh loop) must not block with it.
//
// Each stream has at most one send in flight, on its own goroutine, so the
// goroutines are bounded by the connected agents. A change that arrives while
// a send is in flight marks the stream dirty: when the send returns, one more
// send carries the flags as they are then. A slow agent is not disconnected:
// its send ends when it reads or when its stream ends.
type FeatureFlagDelivery struct {
	agents func(orgID string) []FeatureFlagStream
	flags  func(orgID string) map[string]bool

	mu sync.Mutex
	// inFlight holds the streams with a send running; true when a change
	// arrived during it.
	inFlight map[FeatureFlagStream]bool
}

// NewFeatureFlagDelivery sends flags(orgID) to the streams agents(orgID)
// returns.
func NewFeatureFlagDelivery(agents func(orgID string) []FeatureFlagStream, flags func(orgID string) map[string]bool) *FeatureFlagDelivery {
	return &FeatureFlagDelivery{agents: agents, flags: flags, inFlight: map[FeatureFlagStream]bool{}}
}

// Notify sends the current flags of orgID to each of its agents. It does not
// block.
func (d *FeatureFlagDelivery) Notify(orgID string) {
	for _, s := range d.agents(orgID) {
		if d.claim(s) {
			go d.sendLoop(s)
		}
	}
}

// claim reports true when the caller must run sendLoop for s. With a send in
// flight it marks s dirty instead.
func (d *FeatureFlagDelivery) claim(s FeatureFlagStream) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, busy := d.inFlight[s]; busy {
		d.inFlight[s] = true
		return false
	}
	d.inFlight[s] = false
	return true
}

// sendLoop sends until no change arrived during the last send, or a send
// fails. A failed stream is closing; the agent's next connect seeds it.
func (d *FeatureFlagDelivery) sendLoop(s FeatureFlagStream) {
	for {
		err := d.send(s)
		d.mu.Lock()
		if err != nil || !d.inFlight[s] {
			delete(d.inFlight, s)
			d.mu.Unlock()
			return
		}
		d.inFlight[s] = false
		d.mu.Unlock()
	}
}

func (d *FeatureFlagDelivery) send(s FeatureFlagStream) error {
	data, err := json.Marshal(d.flags(s.GetOrgID()))
	if err != nil {
		log.With("agent", s.AgentName()).Warnf("failed marshalling feature flags: %v", err)
		return err
	}
	err = s.Send(&pb.Packet{
		Type: pbgateway.FeatureFlagUpdate,
		Spec: map[string][]byte{pb.SpecFeatureFlagsKey: data},
	})
	if err != nil {
		log.With("agent", s.AgentName()).Warnf("failed sending feature flag update: %v", err)
	}
	return err
}

var flagDelivery = NewFeatureFlagDelivery(func(orgID string) []FeatureFlagStream {
	streams := streamclient.ListAgentsByOrg(orgID)
	out := make([]FeatureFlagStream, len(streams))
	for i, s := range streams {
		out[i] = s
	}
	return out
}, featureflag.SnapshotForOrg)

// SendFeatureFlagUpdateToOrg sends the current feature flags to every live
// agent stream of the org on this replica. It does not block.
func SendFeatureFlagUpdateToOrg(orgID string) { flagDelivery.Notify(orgID) }

// sendFeatureFlagSeed sends a new agent stream its flags before the stream
// serves anything, as before. It takes the same per-stream slot as the
// updates, so an older seed never lands after a newer update.
func sendFeatureFlagSeed(stream *streamclient.AgentStream) {
	if flagDelivery.claim(stream) {
		flagDelivery.sendLoop(stream)
	}
}
