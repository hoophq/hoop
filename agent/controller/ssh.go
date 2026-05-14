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

	// Hold the session RLock for the duration of the handler. SessionClose
	// takes the Lock side, which drains in-flight handlers before tearing
	// down state; any packet that arrives after cleanup has begun finds
	// closed=true here and returns without touching the store.
	state := a.sessionStateFor(sid)
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.closed.Load() {
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
	clientConnectionIDKey := fmt.Sprintf("%s:%s", sid, clientConnectionID)

	// Fast path: a proxy already exists for this connection. Serialize the
	// write under the per-connection mutex so concurrent handlers for the
	// same (sid, connID) cannot reorder writes to libhoop's upstream.
	if serverWriter, ok := a.connStore.Get(clientConnectionIDKey).(io.WriteCloser); ok {
		writeMu := a.connWriteLockFor(clientConnectionIDKey)
		writeMu.Lock()
		_, err := serverWriter.Write(pkt.Payload)
		writeMu.Unlock()
		if err != nil {
			log.With("sid", sid).Errorf("failed sending packet, err=%v", err)
			a.sendClientSessionClose(sid, fmt.Sprintf("unable to write packet: %v", err))
			_ = serverWriter.Close()
		}
		return
	}

	// Slow path: this is the first packet for the connection. Build the
	// libhoop proxy under singleflight so concurrent first-packets for
	// the same (sid, connID) result in exactly one upstream dial.
	result, err, _ := a.sshFlightGroup.Do(clientConnectionIDKey, func() (any, error) {
		if existing, ok := a.connStore.Get(clientConnectionIDKey).(io.WriteCloser); ok {
			return existing, nil
		}

		connenv, parseErr := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeSSH)
		if parseErr != nil {
			return nil, fmt.Errorf("SSH credentials not found in memory: %v", parseErr)
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
			"connection_id":          clientConnectionID,
		}
		proxy, proxyErr := libhoop.NewSSHProxy(context.Background(), streamClient, opts)
		if proxyErr != nil {
			return nil, fmt.Errorf("failed initializing SSH proxy connection: %v", proxyErr)
		}

		proxy.Run(func(_ int, errMsg string) {
			a.connStore.Del(clientConnectionIDKey)
			a.connWriteLocks.Delete(clientConnectionIDKey)
			a.sendClientSessionClose(sid, errMsg)
		})

		a.connStore.Set(clientConnectionIDKey, proxy)
		return proxy, nil
	})
	if err != nil {
		log.With("sid", sid, "conn", clientConnectionID).Errorf("%v", err)
		a.sendClientSessionClose(sid, err.Error())
		return
	}

	serverWriter, ok := result.(io.WriteCloser)
	if !ok {
		// Unreachable: the singleflight closure always returns an
		// io.WriteCloser or an error. A type mismatch would mean a bug in
		// libhoop's Proxy interface, which we can't continue past.
		log.With("sid", sid, "conn", clientConnectionID).
			Errorf("singleflight returned unexpected type %T", result)
		a.sendClientSessionClose(sid, "internal error: proxy type mismatch")
		return
	}

	// Write the first packet's payload, serialized like the fast path.
	writeMu := a.connWriteLockFor(clientConnectionIDKey)
	writeMu.Lock()
	_, writeErr := serverWriter.Write(pkt.Payload)
	writeMu.Unlock()
	if writeErr != nil {
		errMsg := fmt.Sprintf("unable to connect with remote SSH server, err=%v", writeErr)
		log.With("sid", sid, "conn", clientConnectionID).Errorf(errMsg)
		a.sendClientSessionClose(sid, errMsg)
	}
}
