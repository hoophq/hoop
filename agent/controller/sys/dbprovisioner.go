package controllersys

import (
	"encoding/json"
	"fmt"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbsys "github.com/hoophq/hoop/common/proto/sys"
)

func ProcessDBProvisionerRequest(client pb.ClientTransport, pkt *pb.Packet) {
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	var req pbsys.DBProvisionerRequest
	if err := json.Unmarshal(pkt.Payload, &req); err != nil {
		sendResponseErr(client, sid, "unable to decode payload: %v", err)
		return
	}
	log.With("sid", sid).Infof("received request, type=%v, endpoint=%v, masteruser=%v",
		req.DatabaseType, req.EndpointAddr, req.MasterUsername)
	switch req.DatabaseType {
	case "postgres":
	case "mysql":
	case "mssql":
	default:
		sendResponseErr(client, sid, "database type %q not implemented", req.DatabaseType)
		return
	}

	sendResponseErr(client, sid, "database provisioner not implemented for type %v", req.DatabaseType)
}

func sendResponseOK(client pb.ClientTransport, sid string) {
	payload, pbtype, _ := pbsys.NewDbProvisionerResponse(&pbsys.DBProvisionerResponse{SID: sid})
	_ = client.Send(&pb.Packet{
		Type:    pbtype,
		Payload: payload,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID: []byte(sid),
		},
	})
}

func sendResponseErr(client pb.ClientTransport, sid, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	payload, pbtype, _ := pbsys.NewDbProvisionerResponse(&pbsys.DBProvisionerResponse{SID: sid, ErrorMessage: &msg})
	_ = client.Send(&pb.Packet{
		Type:    pbtype,
		Payload: payload,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID: []byte(sid),
		},
	})
}
