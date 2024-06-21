package cmd

// import (
// 	"context"
// 	_ "embed"
// 	"encoding/json"
// 	"fmt"
// 	"image/color"
// 	"net/http"
// 	"strings"
// 	"time"

// 	"fyne.io/fyne/v2"
// 	"fyne.io/fyne/v2/app"
// 	"fyne.io/fyne/v2/canvas"
// 	"fyne.io/fyne/v2/container"
// 	"fyne.io/fyne/v2/driver/desktop"
// 	"fyne.io/fyne/v2/theme"
// 	"fyne.io/fyne/v2/widget"
// 	clientconfig "github.com/runopsio/hoop/client/config"
// 	proxyconfig "github.com/runopsio/hoop/client/config"
// 	"github.com/runopsio/hoop/client/proxy"
// 	"github.com/runopsio/hoop/common/log"
// 	pb "github.com/runopsio/hoop/common/proto"
// 	pbagent "github.com/runopsio/hoop/common/proto/agent"
// 	pbclient "github.com/runopsio/hoop/common/proto/client"
// )

// //go:embed icon.png
// var AppIconBlack []byte
// var appIconBlackStatic = &fyne.StaticResource{StaticName: "AppIcon", StaticContent: AppIconBlack}

// //go:embed icon_white.png
// var AppIconWhite []byte
// var appIconWhiteStatic = &fyne.StaticResource{StaticName: "AppIconWhite", StaticContent: AppIconWhite}

// //go:embed icon_cable.svg
// var IconCable []byte
// var iconCableStatic = &fyne.StaticResource{StaticName: "AppIconCable", StaticContent: IconCable}

// // go.embed icons8-connected-80.png
// var IconConnect []byte
// var iconConnectStatic = &fyne.StaticResource{StaticName: "AppIconConnect", StaticContent: IconConnect}

// type ConnectionInfo struct {
// 	Name           string   `json:"name"`
// 	ConnectionType string   `json:"type"`
// 	SubType        string   `json:"subtype"`
// 	Status         string   `json:"status"`
// 	Reviewers      []string `json:"reviewers"`

// 	ClientStatus  string
// 	ConnectWidget *widget.Button
// }

// type EventInfo struct {
// 	Index          int
// 	ConnectionName string
// 	ConnectionType string
// 	DbCredentials  string
// 	OnTapSuccess   func()
// }

// type nativeApp struct {
// 	connections         []*ConnectionInfo
// 	connectionStateView fyne.CanvasObject
// 	eventCh             chan EventInfo
// }

// func (a *nativeApp) SendEvent(event EventInfo) {
// 	select {
// 	case a.eventCh <- event:
// 	default:
// 		fmt.Println("unable to send event")
// 	}
// }

// func (a *nativeApp) View() fyne.CanvasObject {
// 	divisor := canvas.NewRectangle(&color.NRGBA{87, 87, 87, 34})
// 	divisor.SetMinSize(fyne.NewSize(2, 2))
// 	gridItems := container.NewAdaptiveGrid(1)
// 	for idx, conn := range a.connections {
// 		connectButton := widget.NewButtonWithIcon("", theme.MediaPlayIcon(), func() {
// 			var dbcred string
// 			switch conn.SubType {
// 			case "postgres":
// 				dbcred = "postgres://noop:noop@127.0.0.1:5433"
// 			case "mysql":
// 				dbcred = "mysql://noop:noop@127.0.0.1:3307"
// 			case "mongodb":
// 				dbcred = "mongodb://noop:noop@127.0.0.1:27018/?directConnection=true"
// 			case "mssql":
// 				dbcred = "sqlserver://noop:noop@127.0.0.1:1444"
// 			}
// 			a.SendEvent(EventInfo{
// 				Index:          idx,
// 				ConnectionName: conn.Name,
// 				ConnectionType: fmt.Sprintf("%s/%s", conn.ConnectionType, conn.SubType),
// 				DbCredentials:  dbcred,
// 				OnTapSuccess: func() {
// 					if conn.ClientStatus == "" {
// 						fmt.Println("disable connect widget ...")
// 						conn.ClientStatus = "ready"
// 					}
// 				}})
// 		})
// 		conn.ConnectWidget = connectButton
// 		if conn.ClientStatus == "ready" {
// 			conn.ConnectWidget.Disable()
// 		}

// 		infoButton := widget.NewButtonWithIcon("", theme.InfoIcon(), func() {})
// 		_ = infoButton
// 		connectButton.Importance = widget.HighImportance
// 		if conn.Status == "offline" || conn.ConnectionType != "database" {
// 			connectButton.Disable()
// 		}

// 		content := container.NewBorder(nil, nil,
// 			widget.NewLabel(conn.Name),
// 			container.NewHBox(
// 				connectButton,
// 				// infoButton,
// 			),
// 		)
// 		gridItems.Add(content)
// 	}
// 	// divisor := canvas.NewRectangle(&color.NRGBA{87, 87, 87, 34})
// 	// divisor.SetMinSize(fyne.NewSize(4, 4))
// 	sc := container.NewScroll(container.NewPadded(container.NewPadded(gridItems)))
// 	sc.SetMinSize(fyne.Size{Width: 540, Height: 440})
// 	return container.NewVBox(a.connectionStateView, sc)
// }

// func makeConnectionInfoView() fyne.CanvasObject {
// 	mainText := widget.NewRichTextFromMarkdown(`
// ## Connect to Resource

// Click in a resource to connect in your private service`)
// 	// img := canvas.NewImageFromResource(iconConnectStatic)
// 	// img.FillMode = canvas.ImageFillContain
// 	// img.SetMinSize(fyne.NewSize(80, 80))
// 	v := container.NewVBox(mainText)
// 	for i := range mainText.Segments {
// 		if seg, ok := mainText.Segments[i].(*widget.TextSegment); ok {
// 			seg.Style.Alignment = fyne.TextAlignCenter
// 		}
// 		if seg, ok := mainText.Segments[i].(*widget.HyperlinkSegment); ok {
// 			seg.Alignment = fyne.TextAlignCenter
// 		}
// 	}
// 	return container.NewPadded(container.NewPadded(container.NewCenter(v)))
// }

// func makeConnectionInfoConnectView(connName, connType, dbCred string, onDisconnect func()) fyne.CanvasObject {
// 	if onDisconnect == nil {
// 		onDisconnect = func() {}
// 	}
// 	mainText := widget.NewRichTextFromMarkdown(fmt.Sprintf(`
// ## %s

// - **type:** %s
// - **status:** ready
// - **credentials:** %s`,
// 		connName, connType, dbCred))

// 	disconnectButton := widget.NewButtonWithIcon("Disconnect", theme.MediaStopIcon(), onDisconnect)
// 	disconnectButton.Importance = widget.DangerImportance
// 	// divisor := canvas.NewRectangle(&color.NRGBA{87, 87, 87, 34})
// 	// divisor.SetMinSize(fyne.NewSize(2, 2))
// 	return container.NewVBox(
// 		container.NewBorder(container.NewPadded(), nil, container.NewPadded(), nil, mainText),
// 		container.NewBorder(nil, nil, container.NewPadded(), container.NewPadded(), disconnectButton),
// 	)
// }

// func runTrayApp() {
// 	a := app.New()
// 	a.Lifecycle().SetOnStarted(func() {
// 		go func() {
// 			time.Sleep(200 * time.Millisecond)
// 			// setActivationPolicy()
// 		}()
// 	})

// 	settingsW := a.NewWindow("Hoop Settings")
// 	settingsW.Resize(fyne.NewSize(560, 460))

// 	settingsW.SetFixedSize(true)
// 	// settingsW.SetContent(content)
// 	settingsW.SetCloseIntercept(func() {
// 		settingsW.Hide()
// 	})
// 	// a.SetIcon(appIconBlackStatic)
// 	connectMenu := fyne.NewMenuItem("Connect ...", func() {
// 		conf := clientconfig.GetClientConfigOrDie()
// 		apiPath := strings.TrimSuffix(conf.ApiURL, "/") + "/api/connections?type=database"
// 		req, _ := http.NewRequest("GET", apiPath, nil)
// 		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", conf.Token))
// 		req.Header.Set("Content-Type", "application/json")
// 		resp, err := http.DefaultClient.Do(req)
// 		if err != nil {
// 			fmt.Printf("failed creating http client, err=%v", err)
// 			a.Quit()
// 			return
// 		}
// 		defer resp.Body.Close()
// 		if resp.StatusCode != 200 {
// 			fmt.Printf("received non ok response from server, status=%v", resp.StatusCode)
// 			a.Quit()
// 			return
// 		}
// 		var connectionItems []*ConnectionInfo
// 		if err := json.NewDecoder(resp.Body).Decode(&connectionItems); err != nil {
// 			fmt.Printf("failed decoding http response, status=%v err=%v", resp.Status, err)
// 			a.Quit()
// 			return
// 		}
// 		appInstance := &nativeApp{
// 			connections:         connectionItems,
// 			connectionStateView: makeConnectionInfoView(),
// 			eventCh:             make(chan EventInfo),
// 		}
// 		currentAppView := appInstance.View()
// 		go func() {
// 			for e := range appInstance.eventCh {
// 				log.Infof("receive connect request to %v, type=%v", e.ConnectionName, e.ConnectionType)
// 				ctxConnect, cancelConnectTimeout := context.WithTimeout(context.Background(), time.Second*20)
// 				onSuccessCallback := func() {
// 					cancelConnectTimeout()
// 				}
// 				ctxDisconnect, cancelDisconectFn := context.WithCancel(context.Background())
// 				ctx, cancelFn := context.WithCancel(context.Background())
// 				go func() {
// 					if err := RunConnectV2(ctxDisconnect, e.ConnectionName, conf, onSuccessCallback); err != nil && err != context.Canceled {
// 						a.SendNotification(&fyne.Notification{
// 							Title:   "Connect Error",
// 							Content: err.Error(),
// 						})
// 						cancelFn()
// 						cancelDisconectFn()
// 					}
// 				}()
// 				<-ctxConnect.Done()
// 				if err := context.Cause(ctxConnect); err != nil && err != context.Canceled {
// 					log.Infof("got timeout connecting to %s, err=%v", e.ConnectionName, err)
// 					a.SendNotification(&fyne.Notification{
// 						Title:   "Timeout Connecting",
// 						Content: fmt.Sprintf("timeout connecting to %v, err=%v", e.ConnectionName, err.Error()),
// 					})
// 					continue
// 				}
// 				log.Infof("connected with success at %v. Waiting connection to finish", e.ConnectionName)
// 				appInstance.connectionStateView = makeConnectionInfoConnectView(
// 					e.ConnectionName,
// 					e.ConnectionType,
// 					e.DbCredentials,
// 					func() {
// 						conn := appInstance.connections[e.Index]
// 						conn.ClientStatus = ""
// 						cancelDisconectFn()
// 						appInstance.connectionStateView = makeConnectionInfoView()
// 						settingsW.SetContent(appInstance.View())
// 					})
// 				conn := appInstance.connections[e.Index]
// 				conn.ClientStatus = "ready"
// 				settingsW.SetContent(appInstance.View())

// 				<-ctx.Done()
// 			}
// 		}()
// 		settingsW.SetContent(currentAppView)
// 		// settingsW.SetContent(makeBoxLayout(out))
// 		settingsW.Show()
// 	})
// 	connectMenu.Icon = iconCableStatic
// 	connectMenu.Disabled = false

// 	loginDisplayMsg := fyne.NewMenuItem("Login to connect on services", func() {})
// 	loginDisplayMsg.Disabled = true

// 	trayMenu := fyne.NewMenu("Hoop Dev",
// 		connectMenu,
// 		fyne.NewMenuItemSeparator(),
// 		loginDisplayMsg,
// 		fyne.NewMenuItemSeparator(),
// 		fyne.NewMenuItem("Log in ...", func() {
// 			conf, err := proxyconfig.Load()
// 			switch err {
// 			case proxyconfig.ErrEmpty:
// 			case nil:
// 				// if the configuration was edited manually
// 				// validate it and prompt for a new one if it's not valid
// 				if !conf.IsValid() {
// 					a.SendNotification(&fyne.Notification{
// 						Title:   "Configuration Invalid",
// 						Content: "configuration is invalid",
// 					})
// 					a.Quit()
// 					return
// 				}
// 			default:
// 				a.SendNotification(&fyne.Notification{
// 					Title:   "Configuration Invalid",
// 					Content: fmt.Sprintf("unable to load configuration, err=%v", err),
// 				})
// 				a.Quit()
// 				return
// 			}
// 			log.Infof("loaded configuration file, mode=%v, grpc_url=%v, api_url=%v, tokenlength=%v",
// 				conf.Mode, conf.GrpcURL, conf.ApiURL, len(conf.Token))
// 			// perform the login and save the token
// 			if conf.ApiURL == "" {
// 				conf.ApiURL = "https://use.hoop.dev"
// 			}
// 			conf.Token, err = doLogin(conf.ApiURL)
// 			if err != nil {
// 				a.SendNotification(&fyne.Notification{
// 					Title:   "Configuration Save Error",
// 					Content: fmt.Sprintf("failed saving local configuration, reason=%v", err),
// 				})
// 				a.Quit()
// 				return
// 			}
// 			if conf.GrpcURL == "" {
// 				// best-effort to obtain the obtain the grpc url
// 				// if it's not set
// 				conf.GrpcURL, err = fetchGrpcURL(conf.ApiURL, conf.Token)
// 				if err != nil {
// 					a.SendNotification(&fyne.Notification{
// 						Title:   "Fetch Grpc URL Error",
// 						Content: fmt.Sprintf("failed fetching grpc url, reason=%v", err),
// 					})
// 					a.Quit()
// 					return
// 				}
// 				log.Infof("obtained remote grpc url %v", conf.GrpcURL)
// 			}
// 			log.Infof("saving token, length=%v", len(conf.Token))
// 			saved, err := conf.Save()
// 			if err != nil {
// 				a.SendNotification(&fyne.Notification{
// 					Title:   "Save Token Error",
// 					Content: fmt.Sprintf("failed saving token local, reason=%v", err),
// 				})
// 				a.Quit()
// 				return
// 			}
// 			if saved {
// 				log.Infof("Login succeeded")
// 			}
// 		}),
// 	)

// 	if desk, ok := a.(desktop.App); ok {

// 		desk.SetSystemTrayIcon(appIconBlackStatic)
// 		desk.SetSystemTrayMenu(trayMenu)
// 	}
// 	a.Run()
// }

// // TODO: make sure to clear all local connections after receiving any error
// func RunConnectV2(ctx context.Context, connection string, config *clientconfig.Config, onSuccessCallback func()) error {
// 	defer onSuccessCallback()
// 	c := newClientConnect(config, nil, []string{connection}, pb.ClientVerbConnect)
// 	sendOpenSessionPktFn := func() error {
// 		spec := newClientArgsSpec(c.clientArgs, nil)
// 		spec[pb.SpecJitTimeout] = []byte(connectFlags.duration)
// 		if err := c.client.Send(&pb.Packet{
// 			Type: pbagent.SessionOpen,
// 			Spec: spec,
// 		}); err != nil {
// 			_, _ = c.client.Close()
// 			return fmt.Errorf("failed opening session with gateway, err=%v", err)
// 		}
// 		return nil
// 	}

// 	go func() {
// 		<-ctx.Done()
// 		for _, obj := range c.connStore.List() {
// 			if srv, ok := obj.(proxy.Closer); ok {
// 				srv.Close()
// 			}
// 			_, _ = c.client.Close()
// 		}
// 	}()

// 	if err := sendOpenSessionPktFn(); err != nil {
// 		return err
// 	}
// 	for {
// 		pkt, err := c.client.Recv()
// 		if err != nil {
// 			return err
// 		}
// 		if pkt == nil {
// 			continue
// 		}
// 		switch pb.PacketType(pkt.Type) {
// 		case pbclient.SessionOpenWaitingApproval:
// 			log.Infof("waiting task to be approved at %v", string(pkt.Payload))
// 		case pbclient.SessionOpenOK:
// 			sessionID, ok := pkt.Spec[pb.SpecGatewaySessionID]
// 			if !ok || sessionID == nil {
// 				return fmt.Errorf("internal error, session not found")
// 			}
// 			onSuccessCallback()
// 			connnectionType := pb.ConnectionType(pkt.Spec[pb.SpecConnectionType])
// 			switch connnectionType {
// 			case pb.ConnectionTypePostgres:
// 				srv := proxy.NewPGServer(c.proxyPort, c.client)
// 				if err := srv.Serve(string(sessionID)); err != nil {
// 					return fmt.Errorf("connect - failed initializing postgres proxy, err=%v", err)
// 				}
// 				c.client.StartKeepAlive()
// 				c.connStore.Set(string(sessionID), srv)
// 			case pb.ConnectionTypeMySQL:
// 				srv := proxy.NewMySQLServer(c.proxyPort, c.client)
// 				if err := srv.Serve(string(sessionID)); err != nil {
// 					return fmt.Errorf("connect - failed initializing mysql proxy, err=%v", err)
// 				}
// 				c.client.StartKeepAlive()
// 				c.connStore.Set(string(sessionID), srv)
// 			case pb.ConnectionTypeMSSQL:
// 				srv := proxy.NewMSSQLServer(c.proxyPort, c.client)
// 				if err := srv.Serve(string(sessionID)); err != nil {
// 					return fmt.Errorf("connect - failed initializing mssql proxy, err=%v", err)
// 				}
// 				c.client.StartKeepAlive()
// 				c.connStore.Set(string(sessionID), srv)
// 			case pb.ConnectionTypeMongoDB:
// 				srv := proxy.NewMongoDBServer(c.proxyPort, c.client)
// 				if err := srv.Serve(string(sessionID)); err != nil {
// 					return fmt.Errorf("connect - failed initializing mongo proxy, err=%v", err)
// 				}
// 				c.client.StartKeepAlive()
// 				c.connStore.Set(string(sessionID), srv)
// 			case pb.ConnectionTypeTCP:
// 				proxyPort := "8999"
// 				if c.proxyPort != "" {
// 					proxyPort = c.proxyPort
// 				}
// 				tcp := proxy.NewTCPServer(proxyPort, c.client, pbagent.TCPConnectionWrite)
// 				if err := tcp.Serve(string(sessionID)); err != nil {
// 					return fmt.Errorf("connect - failed initializing tcp proxy, err=%v", err)
// 				}
// 				c.client.StartKeepAlive()
// 				c.connStore.Set(string(sessionID), tcp)
// 			default:
// 				return fmt.Errorf(`connection type %q not supported`, connnectionType.String())
// 			}
// 		case pbclient.SessionOpenApproveOK:
// 			if err := sendOpenSessionPktFn(); err != nil {
// 				return sendOpenSessionPktFn()
// 			}
// 		case pbclient.SessionOpenAgentOffline:
// 			return pb.ErrAgentOffline
// 		case pbclient.SessionOpenTimeout:
// 			return fmt.Errorf("session ended, reached connection duration (%s)", connectFlags.duration)
// 		// process terminal
// 		case pbclient.PGConnectionWrite:
// 			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
// 			srvObj := c.connStore.Get(string(sessionID))
// 			srv, ok := srvObj.(*proxy.PGServer)
// 			if !ok {
// 				return fmt.Errorf("unable to obtain proxy client from memory")
// 			}
// 			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
// 			_, err := srv.PacketWriteClient(connectionID, pkt)
// 			if err != nil {
// 				return fmt.Errorf("failed writing to client, err=%v", err)
// 			}
// 		case pbclient.MySQLConnectionWrite:
// 			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
// 			srvObj := c.connStore.Get(string(sessionID))
// 			srv, ok := srvObj.(*proxy.MySQLServer)
// 			if !ok {
// 				return fmt.Errorf("unable to obtain proxy client from memory")
// 			}
// 			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
// 			_, err := srv.PacketWriteClient(connectionID, pkt)
// 			if err != nil {
// 				return fmt.Errorf("failed writing to client, err=%v", err)
// 			}
// 		case pbclient.MSSQLConnectionWrite:
// 			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
// 			srvObj := c.connStore.Get(string(sessionID))
// 			srv, ok := srvObj.(*proxy.MSSQLServer)
// 			if !ok {
// 				return fmt.Errorf("unable to obtain proxy client from memory")
// 			}
// 			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
// 			_, err := srv.PacketWriteClient(connectionID, pkt)
// 			if err != nil {
// 				return fmt.Errorf("failed writing to client, err=%v", err)
// 			}
// 		case pbclient.MongoDBConnectionWrite:
// 			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
// 			srvObj := c.connStore.Get(string(sessionID))
// 			srv, ok := srvObj.(*proxy.MongoDBServer)
// 			if !ok {
// 				return fmt.Errorf("unable to obtain proxy client from memory")
// 			}
// 			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
// 			_, err := srv.PacketWriteClient(connectionID, pkt)
// 			if err != nil {
// 				return fmt.Errorf("failed writing to client, err=%v", err)
// 			}
// 		case pbclient.TCPConnectionWrite:
// 			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
// 			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
// 			if tcp, ok := c.connStore.Get(string(sessionID)).(*proxy.TCPServer); ok {
// 				_, err := tcp.PacketWriteClient(connectionID, pkt)
// 				if err != nil {
// 					return fmt.Errorf("failed writing to client, err=%v", err)
// 				}
// 			}
// 		case pbclient.TCPConnectionClose:
// 			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
// 			srvObj := c.connStore.Get(string(sessionID))
// 			if srv, ok := srvObj.(proxy.Closer); ok {
// 				srv.CloseTCPConnection(string(pkt.Spec[pb.SpecClientConnectionID]))
// 			}
// 		case pbclient.SessionClose:
// 			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
// 			if srv, ok := c.connStore.Get(string(sessionID)).(proxy.Closer); ok {
// 				srv.Close()
// 			}
// 		}
// 	}
// }
