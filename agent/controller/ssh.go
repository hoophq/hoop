package controller

import (
	"context"
	"fmt"
	"io"
	"libhoop"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

func (a *Agent) processSSHProtocol(pkt *pb.Packet) {
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	if _, closed := a.closedSessions.Load(sid); closed {
		log.With("sid", sid).Debugf("session already closed, dropping late SSH packet")
		return
	}
	streamClient := pb.NewStreamWriter(a.client, pbclient.SSHConnectionWrite, pkt.Spec)
	connParams := a.connectionParams(sid)
	if connParams == nil {
		log.With("sid", sid).Errorf("connection params not found")
		a.sendClientSessionClose(sid, "connection params not found, contact the administrator")
		return
	}

	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	if clientConnectionID == "" && pkt.Payload != nil {
		log.With("sid", sid).Errorf("connection id not found in memory")
		a.sendClientSessionClose(sid, "connection id not found, contact the administrator")
		return
	}
	clientConnectionIDKey := fmt.Sprintf("%s:%s", sid, string(clientConnectionID))
	clientObj := a.connStore.Get(clientConnectionIDKey)
	if serverWriter, ok := clientObj.(io.WriteCloser); ok {
		if _, err := serverWriter.Write(pkt.Payload); err != nil {
			log.With("sid", sid).Errorf("failed sending packet, err=%v", err)
			a.sendClientSessionClose(sid, fmt.Sprintf("unable to write packet: %v", err))
			_ = serverWriter.Close()
		}
		return
	}

	// SessionClose runs on a different mutex and may have already cleaned up
	// this session. The closedSessions flag is set atomically before cleanup,
	// so checking it here prevents creating a spurious SSH connection from a
	// late-arriving packet.
	if _, closed := a.closedSessions.Load(sid); closed {
		log.With("sid", sid, "conn", clientConnectionID).
			Debugf("session already closed, dropping late SSH packet")
		return
	}

	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeSSH)
	if err != nil {
		log.With("sid", sid).Error("SSH credentials not found in memory, err=%v", err)
		a.sendClientSessionClose(sid, "credentials are empty, contact the administrator")
		return
	}

	log.With("sid", sid, "conn", clientConnectionID).
		Infof("starting SSH proxy connection at %v", connenv.Address())

	opts := map[string]string{
		"sid":                    sid,
		"hostname":               connenv.host,
		"port":                   connenv.port,
		"username":               connenv.user,
		"password":               connenv.pass,
		"authorized_server_keys": connenv.authorizedSSHKeys,
		"ssh_certificate":        connenv.sshCertificate,
		"ssh_private_key":        connenv.sshPrivateKey,
		"connection_id":          clientConnectionID,
	}
	serverWriter, err := libhoop.NewSSHProxy(context.Background(), streamClient, opts)
	if err != nil {
		errMsg := fmt.Sprintf("failed initializing SSH proxy connection, err=%v", err)
		log.With("sid", sid, "conn", clientConnectionID).Errorf(errMsg)
		a.sendClientSessionClose(sid, errMsg)
		return
	}

	serverWriter.Run(func(_ int, errMsg string) {
		a.connStore.Del(clientConnectionIDKey)
		a.sendClientSessionClose(sid, errMsg)
	})

	// write the first packet when establishing the connection
	if _, err = serverWriter.Write(pkt.Payload); err != nil {
		errMsg := fmt.Sprintf("unable to connect with remote SSH server, err=%v", err)
		log.With("sid", sid, "conn", clientConnectionID).Errorf(errMsg)
		a.sendClientSessionClose(sid, errMsg)
		return
	}

	a.connStore.Set(clientConnectionIDKey, serverWriter)
}
