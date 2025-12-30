package controller

import (
	"context"
	"fmt"
	"io"
	"libhoop"
	"sync/atomic"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// httpProxyRequestIDWriter wraps StreamWriter to include request ID in response packet spec
// The request ID is set by the proxy right before each response is written via SetRequestID
// This ensures each response gets the correct request ID even with concurrent requests
type httpProxyRequestIDWriter struct {
	wrapped   io.WriteCloser // The original StreamWriter from NewStreamWriter
	requestID atomic.Value   // stores current request ID (string) - set by proxy before each Write
}

// SetRequestID implements the RequestIDSetter interface from libhoop/agent/httpproxy
// Called by the proxy right before writing each response to set the correct request ID
func (w *httpProxyRequestIDWriter) SetRequestID(requestID string) {
	if requestID != "" {
		log.Debugf("setting request ID for response: %s", requestID)
		w.requestID.Store(requestID)
	}
}

func (w *httpProxyRequestIDWriter) Write(data []byte) (int, error) {
	// Get the stored request ID (set by proxy via SetRequestID before this Write call)
	var requestID string
	if stored := w.requestID.Load(); stored != nil {
		requestID = stored.(string)
	}

	// If we have a request ID, add it to the spec before writing the response
	if requestID != "" {
		if specWriter, ok := w.wrapped.(interface{ AddSpecVal(key string, val []byte) }); ok {
			log.Debugf("adding request ID to response spec: %s", requestID)
			specWriter.AddSpecVal(pb.SpecHttpProxyRequestIDs, []byte(requestID))
		}

		// Delegate to wrapped writer
		return w.wrapped.Write(data)
	}
	log.Infof("no request ID set for response, writing without it")
	return w.wrapped.Write(data)

}

func (w *httpProxyRequestIDWriter) Close() error {
	if w.wrapped != nil {
		return w.wrapped.Close()
	}
	return nil
}

func (a *Agent) processHttpProxyWriteServer(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	proxyBaseURL := string(pkt.Spec[pb.SpecHttpProxyBaseUrl])
	log := log.With("sid", sessionID, "conn", clientConnectionID)
	if clientConnectionID == "" {
		log.Info("connection not found in packet specfication")
		a.sendClientSessionClose(sessionID, "http proxy connection id not found")
		return
	}
	connParams := a.connectionParams(sessionID)
	if connParams == nil {
		log.Infof("connection params not found")
		a.sendClientSessionClose(sessionID, "connection params not found, contact the administrator")
		return
	}
	// check if we already have an existing http proxy connection
	// reusing this connection we need to make sure the proxy can handle multiple requests over the same connection
	// The proxy extracts request ID from each request and sets it via SetRequestID before writing each response
	clientConnectionIDKey := fmt.Sprintf("%s:%s", sessionID, string(clientConnectionID))
	if httpProxy, ok := a.connStore.Get(clientConnectionIDKey).(io.WriteCloser); ok {
		if _, err := httpProxy.Write(pkt.Payload); err != nil {
			log.Infof("failed writing packet, err=%v", err)
			_ = httpProxy.Close()
			a.sendClientSessionClose(sessionID, fmt.Sprintf("failed writing to http proxy connection, reason=%v", err))
		}
		return
	}

	// Create the original StreamWriter
	httpStreamClient := pb.NewStreamWriter(a.client, pbclient.HttpProxyConnectionWrite, pkt.Spec)

	// Wrap it with our request ID writer
	// The proxy will call SetRequestID before each Write to set the correct request ID
	requestIDWriter := &httpProxyRequestIDWriter{
		wrapped: httpStreamClient,
	}

	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeHttpProxy)
	if err != nil {
		log.Infof("missing connection credentials in memory, err=%v", err)
		a.sendClientSessionClose(sessionID, "credentials are empty, contact the administrator")
		return
	}

	log.Infof("starting http proxy connection at %v", connenv.httpProxyRemoteURL)

	connenv.httpProxyHeaders["remote_url"] = connenv.httpProxyRemoteURL
	connenv.httpProxyHeaders["connection_id"] = clientConnectionID
	connenv.httpProxyHeaders["sid"] = sessionID
	connenv.httpProxyHeaders["insecure"] = fmt.Sprintf("%v", connenv.insecure)
	connenv.httpProxyHeaders["proxy_base_url"] = proxyBaseURL

	// Create the proxy with the wrapped writer
	// The proxy extracts X-Hoop-Request-ID from each request and calls SetRequestID before writing responses
	httpProxy, err := libhoop.NewHttpProxy(context.Background(), requestIDWriter, connenv.httpProxyHeaders)
	if err != nil {
		log.Infof("failed connecting to %v, err=%v", connenv.host, err)
		a.sendClientSessionClose(sessionID, fmt.Sprintf("failed connecting to internal service, reason=%v", err))
		return
	}
	// write the first packet when establishing the connection
	_, _ = httpProxy.Write(pkt.Payload)

	// Store the proxy for connection reuse
	a.connStore.Set(clientConnectionIDKey, httpProxy)
}
