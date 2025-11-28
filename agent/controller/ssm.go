package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"libhoop"

	"github.com/aws/session-manager-plugin/src/service"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

func (a *Agent) processSSMProtocol(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	streamClient := pb.NewStreamWriter(a.client, pbclient.SSMConnectionWrite, pkt.Spec)
	connParams := a.connectionParams(sessionID)
	if connParams == nil {
		log.Errorf("session=%s - connection params not found", sessionID)
		a.sendClientSessionClose(sessionID, "connection params not found, contact the administrator")
		return
	}

	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	if clientConnectionID == "" {
		log.Println("connection id not found in memory")
		a.sendClientSessionClose(sessionID, "connection id not found, contact the administrator")
		return
	}

	clientConnectionIDKey := fmt.Sprintf("%s:%s", sessionID, string(clientConnectionID))
	clientObj := a.connStore.Get(clientConnectionIDKey)
	if serverWriter, ok := clientObj.(io.WriteCloser); ok {
		if _, err := serverWriter.Write(pkt.Payload); err != nil {
			log.Errorf("failed sending packet, err=%v", err)
			a.sendClientSessionClose(sessionID, "fail to write packet")
			_ = serverWriter.Close()
		}
		return
	}

	targetInstance := string(pkt.Spec[pb.SpecAwsSSMEc2InstanceId])
	if targetInstance == "" {
		log.Println("missing aws instance id")
		a.sendClientSessionClose(sessionID, "missing aws instance id, contact the administrator")
		return
	}

	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeSSM)
	if err != nil {
		log.Error("AWS SSM credentials not found in memory, err=%v", err)
		a.sendClientSessionClose(sessionID, "credentials are empty, contact the administrator")
		return
	}

	log.Infof("session=%v - starting AWS SSM connection at %v:%v", sessionID, connenv.host, connenv.port)

	var initPacket service.OpenDataChannelInput
	if err := json.Unmarshal(pkt.Payload, &initPacket); err != nil {
		errMsg := fmt.Sprintf("failed connecting with ssm server, err=%v", err)
		log.Errorf(errMsg)
		a.sendClientSessionClose(sessionID, errMsg)
		return
	}

	opts := map[string]string{
		"sid":                   sessionID,
		"aws_access_key_id":     connenv.awsAccessKeyID,
		"aws_secret_access_key": connenv.awsSecretAccessKey,
		"aws_region":            connenv.awsRegion,
		"aws_instance_id":       targetInstance,
	}
	serverWriter, err := libhoop.NewDBCore(context.Background(), streamClient, opts).SSM()
	if err != nil {
		errMsg := fmt.Sprintf("failed connecting with ssm server, err=%v", err)
		log.Errorf(errMsg)
		a.sendClientSessionClose(sessionID, errMsg)
		return
	}
	serverWriter.Run(func(_ int, errMsg string) {
		a.sendClientSessionClose(sessionID, errMsg)
	})
	// write the first packet when establishing the connection
	_, _ = serverWriter.Write(pkt.Payload)
	a.connStore.Set(clientConnectionIDKey, serverWriter)
}
