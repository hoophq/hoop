package dbprovisioner

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"libhoop"
	"sync"
	"time"

	"github.com/hoophq/hoop/agent/secretsmanager"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
)

const maxOutputBytes int = 4096

var memoryStore = memory.New()

func ProcessDBProvisionerRequest(client pb.ClientTransport, pkt *pb.Packet) {
	go processDBProvisionerRequest(client, pkt)
}

func processDBProvisionerRequest(client pb.ClientTransport, pkt *pb.Packet) {
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	var req pbsystem.DBProvisionerRequest
	if err := json.Unmarshal(pkt.Payload, &req); err != nil {
		sendResponse(client, pbsystem.NewError(sid, "unable to decode payload: %v", err))
		return
	}

	// use a lock mechanism to avoid initializing multiple process to the same instance
	lockResourceID := req.OrgID + ":" + req.ResourceID
	if memoryStore.Has(lockResourceID) {
		sendResponse(client, pbsystem.NewError(sid, "process already being executed, resource_id=%v", req.ResourceID))
		return
	}
	memoryStore.Set(lockResourceID, nil)
	defer memoryStore.Del(lockResourceID)

	vault, err := secretsmanager.NewVaultProvider()
	hasVaultProvider := req.Vault != nil
	if hasVaultProvider && err != nil {
		sendResponse(client, pbsystem.NewError(sid, err.Error()))
		return
	}

	log.With("sid", sid).Infof("received provisoning request, type=%v, address=%v, masteruser=%v, vault-provider=%v, runbook-hook=%v",
		req.DatabaseType, req.Address(), req.MasterUsername, hasVaultProvider, req.ExecHook != nil)

	var res *pbsystem.DBProvisionerResponse
	switch req.DatabaseType {
	case "postgres", "aurora-postgresql":
		res = provisionPostgresRoles(req)
	case "mysql", "aurora-mysql":
		res = provisionMySQLRoles(req)
	case "sqlserver-ee", "sqlserver-se", "sqlserver-ex", "sqlserver-web":
		res = provisionMSSQLRoles(req)
	default:
		sendResponse(client, pbsystem.NewError(sid, "database provisioner not implemented for type %q", req.DatabaseType))
		return
	}

	// if the provisioner doesn't set a status, set it to completed
	if res.Status == "" {
		res.Status = pbsystem.StatusCompletedType
		res.Message = pbsystem.MessageCompleted
	}

	// in case of any user provisioning error, set the main status as failed
	for _, item := range res.Result {
		if item.Status != pbsystem.StatusCompletedType {
			res.Message = pbsystem.MessageOneOrMoreRolesFailed
			res.Status = pbsystem.StatusFailedType
			break
		}
	}

	if hasVaultProvider && res.Status == pbsystem.StatusCompletedType {
		for _, item := range res.Result {
			item.Credentials.SecretsManagerProvider = pbsystem.SecretsManagerProviderVault
			item.Credentials.SecretKeys = []string{"HOST", "PORT", "USER", "PASSWORD", "DB"}

			// e.g.: dbsecrets/data/hoop_ro_127.0.0.1
			vaultPath := fmt.Sprintf("%s%s_%s", req.Vault.SecretID, item.Credentials.User, item.Credentials.Host)
			item.Credentials.SecretID = vaultPath
			err := vault.SetValue(vaultPath, map[string]string{
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
				res.Message = pbsystem.MessageVaultSaveError
				res.Status = pbsystem.StatusFailedType
			}
		}
	}

	if req.ExecHook != nil {
		startedExecutionAt := time.Now().UTC()
		stdout, stdoutw := io.Pipe()
		stderr, stderrw := io.Pipe()
		provisionerResponseJSON, _ := json.Marshal(res)
		provisionerRequestJSON, _ := json.Marshal(req)
		cmd, err := libhoop.NewAdHocExec(
			map[string]any{
				"envvar:HOOP_AWS_CONNECT_REQUEST":  base64.StdEncoding.EncodeToString(provisionerRequestJSON),
				"envvar:HOOP_AWS_CONNECT_RESPONSE": base64.StdEncoding.EncodeToString(provisionerResponseJSON),
			},
			req.ExecHook.Command,
			[]byte(req.ExecHook.InputFile),
			stdoutw,
			stderrw,
			nil)
		if err != nil {
			sendResponse(client, pbsystem.NewError(sid, "failed executing runbook hook, reason=%v", err))
			return
		}

		log.With("sid", sid).Infof("starting executing exec runbook, command=%v, filelength=%v",
			req.ExecHook.Command, len(req.ExecHook.InputFile))
		output := &outputSafeWriter{buf: bytes.NewBufferString("")}

		// CAUTION: stdout and stderr streams are not merged based on their actual arrival time.
		// Due to limitations in the underlying terminal package, the output may display stderr
		// content out of sequence relative to stdout. This can make debugging difficult as
		// error messages might appear before or after their triggering output rather than
		// precisely when they occurred during execution.
		stdoutCh := copyBuffer(output, stdout, 4096, "stdout-reader")
		stderrCh := copyBuffer(output, stderr, 4096, "stderr-reader")
		cmd.Run(func(exitCode int, errMsg string) {
			log.With("sid", sid).Infof("finish executing runbook hook on callback, exit_code=%v, output-length=%v, err=%v",
				exitCode, output.Len(), errMsg)
			_ = stdoutw.Close()
			_ = stderrw.Close()

			// truncate at 4096 bytes
			outputContent := output.String()
			if len(outputContent) > maxOutputBytes {
				remainingBytes := len(outputContent[maxOutputBytes:])
				outputContent = outputContent[:maxOutputBytes]
				outputContent += fmt.Sprintf(" [truncated %v byte(s)]", remainingBytes)
			}
			res.RunbookHook = &pbsystem.RunbookHook{
				ExitCode:         exitCode,
				Output:           outputContent,
				ExecutionTimeSec: int(time.Since(startedExecutionAt).Seconds()),
			}
		})
		<-stdoutCh
		<-stderrCh

	}

	sendResponse(client, res)
}

func sendResponse(client pb.ClientTransport, response *pbsystem.DBProvisionerResponse) {
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
	for i := range passwordLength {
		// Use modulo to map the random byte to an index in the charset
		// This ensures the mapping is within the charset boundaries
		password[i] = charset[int(password[i])%len(charset)]
	}

	return string(password), nil
}

func copyBuffer(dst io.Writer, src io.Reader, bufSize int, stream string) chan struct{} {
	doneCh := make(chan struct{})
	go func() {
		wb, err := io.CopyBuffer(dst, src, make([]byte, bufSize))
		log.Infof("[%s] - done copying runbook stream, written=%v, err=%v", stream, wb, err)
		close(doneCh)
	}()
	return doneCh
}

type outputSafeWriter struct {
	buf *bytes.Buffer
	mu  sync.Mutex
}

func (w *outputSafeWriter) Write(data []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(data)
}

func (w *outputSafeWriter) String() string { return w.buf.String() }
func (w *outputSafeWriter) Len() int       { return w.buf.Len() }
