package runbooks

import (
	"context"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/user"
)

const nilExitCode = -100

type clientExecResponse struct {
	err      error
	exitCode int
}

type pluginService interface {
	FindOne(context *user.Context, name string) (*plugin.Plugin, error)
}

type connectionService interface {
	FindOne(context *user.Context, name string) (*connection.Connection, error)
}

func getAccessToken(c *gin.Context) string {
	tokenHeader := c.GetHeader("authorization")
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) > 1 {
		return tokenParts[1]
	}
	return ""
}

func processClientExec(inputPayload []byte, encodedEnvVars []byte, client pb.ClientTransport) (int, error) {
	sendOpenSessionPktFn := func() error {
		return client.Send(&pb.Packet{
			Type:    pbagent.SessionOpen,
			Payload: inputPayload,
			Spec: map[string][]byte{
				pb.SpecClientExecEnvVar: encodedEnvVars,
			},
		})
	}
	if err := sendOpenSessionPktFn(); err != nil {
		return nilExitCode, err
	}
	ctxTimeout, cancel := context.WithTimeout(context.Background(), time.Hour*4)
	defer cancel()
	for {
		pkt, err := client.Recv()
		if err != nil {
			if err == io.EOF {
				return nilExitCode, nil
			}
			return nilExitCode, err
		}
		if pkt == nil {
			continue
		}
		log.Printf("processing packet %v", pkt.Type)
		switch pkt.Type {
		case pbclient.SessionOpenWaitingApproval:
			log.Printf("waiting task to be approved at %v", string(pkt.Payload))
			go func() {
				// It prevents reviewed sessions to stay open forever.
				// Closing the client will make the Recv method to fail
				<-ctxTimeout.Done()
				log.Printf("task timeout, closing gRPC client ...")
				client.Close()
			}()
		case pbclient.SessionOpenApproveOK:
			if err := sendOpenSessionPktFn(); err != nil {
				return nilExitCode, err
			}
		case pbclient.SessionOpenAgentOffline:
			return nilExitCode, fmt.Errorf("agent is offline")
		case pbclient.SessionOpenOK:
			stdinPkt := &pb.Packet{
				Type:    pbagent.ExecWriteStdin,
				Payload: inputPayload,
				Spec: map[string][]byte{
					pb.SpecGatewaySessionID: pkt.Spec[pb.SpecGatewaySessionID],
				},
			}
			if err := client.Send(stdinPkt); err != nil {
				return nilExitCode, fmt.Errorf("failed executing command, err=%v", err)
			}
		case pbclient.WriteStdout,
			pbclient.WriteStderr:
			// noop
		case pbclient.SessionClose:
			var execErr error
			if len(pkt.Payload) > 0 {
				execErr = fmt.Errorf(string(pkt.Payload))
			}
			exitCode, err := strconv.Atoi(string(pkt.Spec[pb.SpecClientExitCodeKey]))
			if err != nil {
				exitCode = nilExitCode
			}
			return exitCode, execErr
		default:
			return nilExitCode, fmt.Errorf("packet type %v not implemented", pkt.Type)
		}
	}
}
