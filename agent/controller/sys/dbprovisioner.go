package controllersys

import (
	"crypto/rand"
	"encoding/json"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	pbsys "github.com/hoophq/hoop/common/proto/sys"
)

var memoryStore = memory.New()

func ProcessDBProvisionerRequest(client pb.ClientTransport, pkt *pb.Packet) {
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	var req pbsys.DBProvisionerRequest
	if err := json.Unmarshal(pkt.Payload, &req); err != nil {
		sendResponse(client, pbsys.NewError(sid, "unable to decode payload: %v", err))
		return
	}

	// use a lock mechanism to avoid initializing multiple process to the same instance
	lockResourceID := req.OrgID + ":" + req.ResourceID
	if memoryStore.Has(lockResourceID) {
		sendResponse(client, pbsys.NewError(sid, "process already being executed, resource_id=%v", req.ResourceID))
		return
	}
	memoryStore.Set(lockResourceID, nil)
	defer memoryStore.Del(lockResourceID)

	log.With("sid", sid).Infof("received provisoning request, type=%v, address=%v, masteruser=%v",
		req.DatabaseType, req.Address(), req.MasterUsername)

	var res *pbsys.DBProvisionerResponse
	switch req.DatabaseType {
	case "postgres":
		res = provisionPostgresRoles(req)
	case "mysql":
		res = provisionMySQLRoles(req)
	case "sqlserver-ee", "sqlserver-se", "sqlserver-ex", "sqlserver-web":
		res = provisionMSSQLRoles(req)
	default:
		sendResponse(client, pbsys.NewError(sid, "database provisioner not implemented for type %q", req.DatabaseType))
		return
	}

	// if the provisioner doesn't set a status, set it to completed
	if res.Status == "" {
		res.Status = pbsys.StatusCompletedType
		res.Message = pbsys.MessageCompleted
	}

	// in case of any user provisioning error, set the main status as failed
	for _, item := range res.Result {
		if item.Status != pbsys.StatusCompletedType {
			res.Message = pbsys.MessageOneOrMoreRolesFailed
			res.Status = pbsys.StatusFailedType
			break
		}
	}

	sendResponse(client, res)
}

func sendResponse(client pb.ClientTransport, response *pbsys.DBProvisionerResponse) {
	payload, pbtype, _ := response.Encode()
	_ = client.Send(&pb.Packet{
		Type:    pbtype,
		Payload: payload,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID: []byte(response.SID),
		},
	})
}

func generateRandomPassword() (string, error) {
	// Character set for passwords (lowercase, uppercase, numbers, special chars)
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789*_"
	passwordLength := 25

	// Create a byte slice to store the password
	password := make([]byte, passwordLength)

	// Generate random bytes
	_, err := rand.Read(password)
	if err != nil {
		return "", err
	}

	// Map random bytes to characters in the charset
	for i := 0; i < passwordLength; i++ {
		// Use modulo to map the random byte to an index in the charset
		// This ensures the mapping is within the charset boundaries
		password[i] = charset[int(password[i])%len(charset)]
	}

	return string(password), nil
}
