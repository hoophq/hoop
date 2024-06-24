package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	proxyconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/client/proxy"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	"github.com/runopsio/hoop/common/version"
)

type Connection struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
}

type UserInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// type Session struct {
// 	ID         string `json:"id"`
// 	Connection string `json:"connection"`
// 	Verb       string `json:"verb"`
// }

// type SessionItems struct {
// 	Data []Session `json:"data"`
// }

// func getUserInfo(apiURL, accessToken string) (*UserInfo, error) {
// 	url := fmt.Sprintf("%s/api/userinfo", apiURL)
// 	req, err := http.NewRequest("GET", url, nil)
// 	if err != nil {
// 		return nil, err
// 	}
// 	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
// 	resp, err := http.DefaultClient.Do(req)
// 	if err != nil {
// 		return nil, err
// 	}
// 	if resp.StatusCode == 200 {
// 		var uinfo UserInfo
// 		if err := json.NewDecoder(resp.Body).Decode(&uinfo); err != nil {
// 			return nil, fmt.Errorf("failed decoding userinfo: %v", err)
// 		}
// 		return &uinfo, nil
// 	}
// 	data, _ := io.ReadAll(resp.Body)
// 	return nil, fmt.Errorf("failed fetching user info, status=%v, response=%v", resp.StatusCode, string(data))
// }

func List(apiURL, accessToken string) ([]*Connection, error) {
	url := fmt.Sprintf("%s/api/connections", apiURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 200 {
		var connections []*Connection
		if err := json.NewDecoder(resp.Body).Decode(&connections); err != nil {
			return nil, fmt.Errorf("failed decoding userinfo: %v", err)
		}
		var newItems []*Connection
		for _, conn := range connections {
			if conn.Type == "database" || conn.Subtype == "tcp" {
				newItems = append(newItems, conn)
			}
		}
		return newItems, nil
	}
	data, _ := io.ReadAll(resp.Body)
	return nil, fmt.Errorf("failed fetching user info, status=%v, response=%v", resp.StatusCode, string(data))
}

func newClient(config *proxyconfig.Config, connectionName string) (pb.ClientTransport, error) {
	grpcClientOptions := []*grpc.ClientOptions{
		grpc.WithOption(grpc.OptionConnectionName, connectionName),
		grpc.WithOption("origin", pb.ConnectionOriginClient),
		grpc.WithOption("verb", pb.ClientVerbConnect),
	}
	clientConfig, err := config.GrpcClientConfig()
	if err != nil {
		return nil, err
	}
	clientConfig.UserAgent = fmt.Sprintf("hoopcli/%v", version.Get().Version)
	return grpc.Connect(clientConfig, grpcClientOptions...)
}

func Run(ctx context.Context, connection string, config *proxyconfig.Config, onSuccessCallback func()) error {
	defer onSuccessCallback()
	connStore := memory.New()
	client, err := newClient(config, connection)
	if err != nil {
		return err
	}
	sendOpenSessionPktFn := func() error {
		if err := client.Send(&pb.Packet{
			Type: pbagent.SessionOpen,
			Spec: map[string][]byte{pb.SpecJitTimeout: []byte(`30m`)},
		}); err != nil {
			_, _ = client.Close()
			return fmt.Errorf("failed opening session with gateway, err=%v", err)
		}
		return nil
	}

	go func() {
		<-ctx.Done()
		for _, obj := range connStore.List() {
			if srv, ok := obj.(proxy.Closer); ok {
				srv.Close()
			}
			_, _ = client.Close()
		}

	}()

	if err := sendOpenSessionPktFn(); err != nil {
		return err
	}
	for {
		pkt, err := client.Recv()
		if err != nil {
			return err
		}
		if pkt == nil {
			continue
		}
		switch pb.PacketType(pkt.Type) {
		case pbclient.SessionOpenWaitingApproval:
			log.Printf("waiting task to be approved at %v", string(pkt.Payload))
		case pbclient.SessionOpenOK:
			sessionID, ok := pkt.Spec[pb.SpecGatewaySessionID]
			if !ok || sessionID == nil {
				return fmt.Errorf("internal error, session not found")
			}
			onSuccessCallback()
			client.StartKeepAlive()
			connnectionType := pb.ConnectionType(pkt.Spec[pb.SpecConnectionType])
			switch connnectionType {
			case pb.ConnectionTypePostgres:
				srv := proxy.NewPGServer("5433", client)
				if err := srv.Serve(string(sessionID)); err != nil {
					return fmt.Errorf("connect - failed initializing postgres proxy, err=%v", err)
				}
				connStore.Set(string(sessionID), srv)
			case pb.ConnectionTypeMySQL:
				srv := proxy.NewMySQLServer("3307", client)
				if err := srv.Serve(string(sessionID)); err != nil {
					return fmt.Errorf("connect - failed initializing mysql proxy, err=%v", err)
				}
				connStore.Set(string(sessionID), srv)
			case pb.ConnectionTypeMSSQL:
				srv := proxy.NewMSSQLServer("1433", client)
				if err := srv.Serve(string(sessionID)); err != nil {
					return fmt.Errorf("connect - failed initializing mssql proxy, err=%v", err)
				}
				connStore.Set(string(sessionID), srv)
			case pb.ConnectionTypeMongoDB:
				srv := proxy.NewMongoDBServer("27018", client)
				if err := srv.Serve(string(sessionID)); err != nil {
					return fmt.Errorf("connect - failed initializing mongo proxy, err=%v", err)
				}
				connStore.Set(string(sessionID), srv)
			case pb.ConnectionTypeTCP:
				tcp := proxy.NewTCPServer("8999", client, pbagent.TCPConnectionWrite)
				if err := tcp.Serve(string(sessionID)); err != nil {
					return fmt.Errorf("connect - failed initializing tcp proxy, err=%v", err)
				}
				connStore.Set(string(sessionID), tcp)
			default:
				return fmt.Errorf(`connection type %q not supported`, connnectionType.String())
			}
		case pbclient.SessionOpenApproveOK:
			if err := sendOpenSessionPktFn(); err != nil {
				return sendOpenSessionPktFn()
			}
		case pbclient.SessionOpenAgentOffline:
			return pb.ErrAgentOffline
		case pbclient.SessionOpenTimeout:
			return fmt.Errorf("session ended, reached connection duration")
		// process terminal
		case pbclient.PGConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.PGServer)
			if !ok {
				return fmt.Errorf("unable to obtain proxy client from memory")
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := srv.PacketWriteClient(connectionID, pkt)
			if err != nil {
				return fmt.Errorf("failed writing to client, err=%v", err)
			}
		case pbclient.MySQLConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.MySQLServer)
			if !ok {
				return fmt.Errorf("unable to obtain proxy client from memory")
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := srv.PacketWriteClient(connectionID, pkt)
			if err != nil {
				return fmt.Errorf("failed writing to client, err=%v", err)
			}
		case pbclient.MSSQLConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.MSSQLServer)
			if !ok {
				return fmt.Errorf("unable to obtain proxy client from memory")
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := srv.PacketWriteClient(connectionID, pkt)
			if err != nil {
				return fmt.Errorf("failed writing to client, err=%v", err)
			}
		case pbclient.MongoDBConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.MongoDBServer)
			if !ok {
				return fmt.Errorf("unable to obtain proxy client from memory")
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := srv.PacketWriteClient(connectionID, pkt)
			if err != nil {
				return fmt.Errorf("failed writing to client, err=%v", err)
			}
		case pbclient.TCPConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if tcp, ok := connStore.Get(string(sessionID)).(*proxy.TCPServer); ok {
				_, err := tcp.PacketWriteClient(connectionID, pkt)
				if err != nil {
					return fmt.Errorf("failed writing to client, err=%v", err)
				}
			}
		case pbclient.TCPConnectionClose:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := connStore.Get(string(sessionID))
			if srv, ok := srvObj.(proxy.Closer); ok {
				srv.CloseTCPConnection(string(pkt.Spec[pb.SpecClientConnectionID]))
			}
		case pbclient.SessionClose:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			if srv, ok := connStore.Get(string(sessionID)).(proxy.Closer); ok {
				srv.Close()
			}
		}
	}
}
