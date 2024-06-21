package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	"github.com/golang-jwt/jwt/v5"
	"github.com/runopsio/hoop/client/cmd"
	clientconfig "github.com/runopsio/hoop/client/config"
	proxyconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/trayapp/connect"
	"github.com/runopsio/hoop/trayapp/login"
)

//go:embed assets/icon.png
var AppIconBlack []byte
var appIconBlackStatic = &fyne.StaticResource{StaticName: "AppIcon", StaticContent: AppIconBlack}

//go:embed assets/icon_white.png
var AppIconWhite []byte
var appIconWhiteStatic = &fyne.StaticResource{StaticName: "AppIconWhite", StaticContent: AppIconWhite}

//go:embed assets/icon_cable.svg
var IconCable []byte
var iconCableStatic = &fyne.StaticResource{StaticName: "AppIconCable", StaticContent: IconCable}

// go.embed assets/icons8-connected-80.png
var IconConnect []byte
var iconConnectStatic = &fyne.StaticResource{StaticName: "AppIconConnect", StaticContent: IconConnect}

type CustomClaims struct {
	jwt.RegisteredClaims
}

func validateJwtExpiration(accessToken string) (valid bool, err error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return false, fmt.Errorf("invalid token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		fmt.Println("Error decoding payload:", err)
		return
	}
	var claims CustomClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return false, fmt.Errorf("failed decoding claims: %v", err)
	}
	return claims.ExpiresAt != nil && claims.ExpiresAt.After(time.Now()), nil
}

func loadAccessToken(apiURL, accessToken string) (string, error) {
	if accessToken != "" {
		isValid, err := validateJwtExpiration(accessToken)
		if err != nil {
			return "", fmt.Errorf("failed validating token expiration: %v", err)
		}
		if isValid {
			return accessToken, nil
		}
	}
	return login.Authenticate(apiURL)
}

func appNotifyOk(app fyne.App, format string, a ...any) {
	app.SendNotification(&fyne.Notification{
		Title:   "Hoop Notification",
		Content: fmt.Sprintf(fmt.Sprintf("✅  %s", format), a...),
	})
}

func appNotifyErr(app fyne.App, format string, a ...any) {
	app.SendNotification(&fyne.Notification{
		Title:   "",
		Content: fmt.Sprintf(fmt.Sprintf("❌  %s", format), a...),
	})
}

func connectionListMenu(item1, item2 string) *fyne.Menu {
	// item := fyne.NewMenuItem("Context", func() {

	// })
	return fyne.NewMenu("",
		fyne.NewMenuItem(item1, func() {

		}),
		fyne.NewMenuItem(item2, func() {

		}),
	)
	// return item
}

type Event struct {
	Name    string
	Context string
}

const (
	ContextLocal = "localhost"
	ContextHoop  = "use.hoop.dev"
)

func credentialsForContext(context map[string][]string) (apiURL, grpcURL, token string) {
	for ctxName, val := range context {
		if val[0] != "current" {
			continue
		}
		switch ctxName {
		case ContextLocal:
			return "http://" + ContextLocal + ":8009", ContextLocal + ":8010", val[1]
		case ContextHoop:
			return "https://" + ContextHoop, ContextHoop + ":8443", val[1]
		}
	}
	return
}

func main() {
	a := app.New()
	a.Lifecycle().SetOnStarted(func() {
		go func() {
			time.Sleep(200 * time.Millisecond)
			// setActivationPolicy()
		}()
	})
	appContext := map[string][]string{
		"use.hoop.dev": {"", ""},
		"localhost":    {"", ""},
	}
	// var accessToken string
	// defer a.Quit()

	settingsW := a.NewWindow("Hoop Settings")
	settingsW.Resize(fyne.NewSize(560, 460))

	settingsW.SetFixedSize(true)
	// settingsW.SetContent(content)
	settingsW.SetCloseIntercept(func() {
		settingsW.Hide()
	})

	// a.SetIcon(appIconBlackStatic)
	// currentAppView := appInstance.View()
	connectMenu := fyne.NewMenuItem("Connect ...", func() {
		fmt.Println(appContext)
		// fmt.Println("ACCESS-TOKEN", accessToken)
		// settingsW.SetContent(currentAppView)
		// settingsW.SetContent(makeBoxLayout(out))
		// settingsW.Show()
	})
	// connectMenu.ChildMenu =
	connectMenu.ChildMenu = connectionListMenu("conn01", "conn02")
	connectMenu.Icon = iconCableStatic
	connectMenu.Disabled = false

	loginDisplayMsg := fyne.NewMenuItem("Login to connect on services", func() {})
	loginDisplayMsg.Disabled = true

	eventCh := make(chan Event)
	trayMenu := fyne.NewMenu("Hoop Dev",
		connectMenu,
		fyne.NewMenuItemSeparator(),
		loginDisplayMsg,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Log in (use.hoop.dev) ...", func() {
			token, err := loadAccessToken("https://"+ContextHoop, appContext[ContextHoop][1])
			if err != nil {
				appNotifyErr(a, "failed loading config, reason=%v", err)
				return
			}
			appContext[ContextLocal][0] = ""
			appContext[ContextHoop][0] = "current"
			appContext[ContextHoop][1] = token
			eventCh <- Event{"login", ContextHoop}
			// connectMenu.ChildMenu = connectionListMenu("pg-hoop-rw", "pg-hoop-ro")
			// connectMenu.ChildMenu.Refresh()
		}),
		fyne.NewMenuItem("Log in (localhost) ...", func() {
			token, err := loadAccessToken("http://localhost:8009", appContext[ContextLocal][1])
			if err != nil {
				appNotifyErr(a, "failed loading config, reason=%v", err)
				return
			}
			appContext[ContextHoop][0] = ""
			appContext[ContextLocal][0] = "current"
			appContext[ContextLocal][1] = token
			eventCh <- Event{"login", ContextLocal}
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Save Config", func() {
			config, err := proxyconfig.Load()
			if err != nil && err != proxyconfig.ErrEmpty {
				appNotifyErr(a, "unable to save config, reason=%v", err.Error())
				return
			}
			var contextName string
			for name, val := range appContext {
				if val[0] != "current" {
					continue
				}
				contextName = name
				switch name {
				case ContextHoop:
					config.ApiURL = "https://" + ContextHoop
					config.GrpcURL = ContextHoop + ":8443"
				case ContextLocal:
					config.ApiURL = "http://" + ContextLocal + ":8009"
					config.GrpcURL = ContextLocal + ":8010"
				}
				config.Token = val[1]
			}
			filepath, err := proxyconfig.NewConfigFile(config.ApiURL, config.GrpcURL, config.Token)
			if err != nil {
				appNotifyErr(a, "failed loading config, reason=%v", err)
				return
			}
			if contextName != "" {
				appNotifyOk(a, "context %v saved at %v", contextName, filepath)
			}
		}),
		// contextMenu(),
	)

	go func() {
		var currentCancelFn context.CancelFunc
		for range eventCh {
			apiURL, grpcURL, accessToken := credentialsForContext(appContext)
			connections, err := connect.List(apiURL, accessToken)
			if err != nil {
				appNotifyErr(a, "failed listing connection: %v", err)
				continue
			}

			var menuItems []*fyne.MenuItem
			for _, conn := range connections {
				menuItems = append(menuItems, fyne.NewMenuItem(conn.Name, func() {
					if currentCancelFn != nil {
						currentCancelFn()
					}
					ctx, cancelFn := context.WithCancel(context.Background())
					currentCancelFn = cancelFn
					defer cancelFn()
					clientConfig := &clientconfig.Config{Token: accessToken, ApiURL: apiURL, GrpcURL: grpcURL}
					onSuccess := func() { appNotifyOk(a, "connected %v", conn.Name) }
					if err := cmd.RunConnectV2(ctx, conn.Name, clientConfig, onSuccess); err != nil {
						appNotifyErr(a, "%s: failed with error: %v", conn.Name, err)
					}
				}))
			}
			fyne.NewMenu("Connect", menuItems...)
			connectMenu.ChildMenu = fyne.NewMenu("Connect", menuItems...)
			trayMenu.Refresh()
		}
	}()

	if desk, ok := a.(desktop.App); ok {
		desk.SetSystemTrayIcon(appIconBlackStatic)
		desk.SetSystemTrayMenu(trayMenu)
	}
	a.Run()
}
