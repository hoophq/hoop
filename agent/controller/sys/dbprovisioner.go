package controllersys

import (
	"crypto/rand"
	"encoding/json"
	"fmt"

	"github.com/hoophq/hoop/agent/secretsmanager"
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

	vault, err := secretsmanager.NewVaultProvider()
	hasVaultProvider := req.Vault != nil
	if hasVaultProvider && err != nil {
		sendResponse(client, pbsys.NewError(sid, err.Error()))
		return
	}

	log.With("sid", sid).Infof("received provisoning request, type=%v, address=%v, masteruser=%v, vault-provider=%v",
		req.DatabaseType, req.Address(), req.MasterUsername, hasVaultProvider)

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

	if hasVaultProvider && res.Status == pbsys.StatusCompletedType {
		for _, item := range res.Result {
			item.Credentials.SecretsManagerProvider = pbsys.SecretsManagerProviderVault
			item.Credentials.SecretKeys = []string{"HOST", "PORT", "USER", "PASSWORD", "DB"}
			item.Credentials.SecretID = req.Vault.SecretID
			err := vault.SetValue(req.Vault.SecretID, map[string]string{
				"HOST":     item.Credentials.Host,
				"PORT":     item.Credentials.Port,
				"USER":     item.Credentials.User,
				"PASSWORD": item.Credentials.Password,
				"DB":       item.Credentials.DefaultDatabase,
			})

			// avoid password from being sent by the network when Vault is set
			item.Credentials.Password = ""
			if err != nil {
				item.Message = fmt.Sprintf("Unable to create or update secret in Vault, reason=%v", err)
				res.Message = pbsys.MessageVaultSaveError
				res.Status = pbsys.StatusFailedType
			}
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
