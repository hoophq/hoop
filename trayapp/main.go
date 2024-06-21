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
	proxyconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/trayapp/connect"
	"github.com/runopsio/hoop/trayapp/login"
)

//go:embed assets/icon.png
var AppIconBlack []byte
var appIconBlackStatic = &fyne.StaticResource{StaticName: "AppIcon", StaticContent: AppIconBlack}

//go:embed assets/icon_cable.svg
var IconCable []byte
var iconCableStatic = &fyne.StaticResource{StaticName: "AppIconCable", StaticContent: IconCable}

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

type Event struct {
	Name    string
	Context string
}

const (
	ContextLocal     = "localhost"
	ContextHoop      = "use.hoop.dev"
	ContextTryRunops = "tryrunops.hoop.dev"
)

type GatewayContext struct {
	Name        string
	ApiURL      string
	GrpcURL     string
	AccessToken string
}

func credentialsForContext(context map[string][]string) (ctx GatewayContext) {
	for ctxName, val := range context {
		if val[0] != "current" {
			continue
		}
		ctx.Name = ctxName
		ctx.AccessToken = val[1]
		switch ctxName {
		case ContextLocal:
			ctx.ApiURL = "http://" + ContextLocal + ":8009"
			ctx.GrpcURL = ContextLocal + ":8010"
		case ContextHoop:
			ctx.ApiURL = "https://" + ContextHoop
			ctx.GrpcURL = ContextHoop + ":8443"
		case ContextTryRunops:
			ctx.ApiURL = "https://" + ContextTryRunops
			ctx.GrpcURL = ContextTryRunops + ":8443"
		}
	}
	return
}

func SetNewContext(appContext map[string][]string, contextName, token string) {
	// clear all contexts
	for _, ctx := range []string{ContextLocal, ContextTryRunops, ContextHoop} {
		appContext[ctx][0] = ""
	}

	// set new context
	appContext[contextName][0] = "current"
	appContext[contextName][1] = token
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
		"use.hoop.dev":       {"", ""},
		"localhost":          {"", ""},
		"tryrunops.hoop.dev": {"", ""},
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
	connectMenu.Icon = iconCableStatic
	connectMenu.Disabled = true
	loginDisplayMsg := fyne.NewMenuItem("Login to connect on services", func() {})
	loginDisplayMsg.Disabled = true

	eventCh := make(chan Event)
	menuHoop := fyne.NewMenuItem("Log in (use.hoop.dev) ...", func() {
		token, err := loadAccessToken("https://"+ContextHoop, appContext[ContextHoop][1])
		if err != nil {
			appNotifyErr(a, "failed loading config, reason=%v", err)
			return
		}
		SetNewContext(appContext, ContextHoop, token)
		eventCh <- Event{"login", ContextHoop}
	})
	menuTryRunops := fyne.NewMenuItem("Log in (tryrunops.hoop.dev) ...", func() {
		token, err := loadAccessToken("https://"+ContextTryRunops, appContext[ContextTryRunops][1])
		if err != nil {
			appNotifyErr(a, "failed loading config, reason=%v", err)
			return
		}
		SetNewContext(appContext, ContextTryRunops, token)
		eventCh <- Event{"login", ContextTryRunops}
	})
	menuLocalhost := fyne.NewMenuItem("Log in (localhost) ...", func() {
		token, err := loadAccessToken("http://localhost:8009", appContext[ContextLocal][1])
		if err != nil {
			appNotifyErr(a, "failed loading config, reason=%v", err)
			return
		}
		SetNewContext(appContext, ContextLocal, token)
		eventCh <- Event{"login", ContextLocal}
	})

	trayMenu := fyne.NewMenu("Hoop Dev",
		connectMenu,
		fyne.NewMenuItemSeparator(),
		loginDisplayMsg,
		fyne.NewMenuItemSeparator(),
		menuHoop,
		menuLocalhost,
		menuTryRunops,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Save Config", func() {
			config, err := proxyconfig.Load()
			if err != nil && err != proxyconfig.ErrEmpty {
				appNotifyErr(a, "unable to save config, reason=%v", err.Error())
				return
			}
			ctx := credentialsForContext(appContext)
			config.ApiURL = ctx.ApiURL
			config.GrpcURL = ctx.GrpcURL
			config.Token = ctx.AccessToken
			filepath, err := proxyconfig.NewConfigFile(config.ApiURL, config.GrpcURL, config.Token)
			if err != nil {
				appNotifyErr(a, "failed loading config, reason=%v", err)
				return
			}
			if ctx.Name != "" {
				appNotifyOk(a, "context %v saved at %v", ctx.Name, filepath)
			}
		}),
	)

	go func() {
		var currentCancelFn context.CancelFunc
		for range eventCh {
			gwctx := credentialsForContext(appContext)
			connections, err := connect.List(gwctx.ApiURL, gwctx.AccessToken)
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
					clientConfig := &proxyconfig.Config{Token: gwctx.AccessToken, ApiURL: gwctx.ApiURL, GrpcURL: gwctx.GrpcURL}
					onSuccess := func() { appNotifyOk(a, "connected %v", conn.Name) }
					if err := cmd.RunConnectV2(ctx, conn.Name, clientConfig, onSuccess); err != nil {
						appNotifyErr(a, "%s: failed with error: %v", conn.Name, err)
					}
				}))
			}
			connectDisplayMsg := fyne.NewMenuItem(fmt.Sprintf("Click to connect (%s)", gwctx.Name), func() {})
			connectDisplayMsg.Disabled = true
			menuItems = append([]*fyne.MenuItem{connectDisplayMsg, fyne.NewMenuItemSeparator()}, menuItems...)
			connectMenu.ChildMenu = fyne.NewMenu("", menuItems...)
			connectMenu.Disabled = false
			trayMenu.Refresh()
		}
	}()

	if desk, ok := a.(desktop.App); ok {
		desk.SetSystemTrayIcon(appIconBlackStatic)
		desk.SetSystemTrayMenu(trayMenu)
	}
	a.Run()
}
