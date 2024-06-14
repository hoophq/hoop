package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/runopsio/hoop/client/cmd"
	"github.com/runopsio/hoop/client/cmd/static"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
)

func main() {
	a := app.New()
	w := a.NewWindow("SysTray")

	if desk, ok := a.(desktop.App); ok {
		m := fyne.NewMenu("Hoop Dev",
			fyne.NewMenuItem("login", func() {
				doLogin("https://use.hoop.dev")
			}),
			fyne.NewMenuItem("connect", func() {
				fmt.Println("starting connect ...")
				cmd.RunConnect([]string{"k8s-prod-apiserver"}, nil)
				fmt.Println("end connect ...")
			}),
		)

		desk.SetSystemTrayMenu(m)
	}

	w.SetContent(widget.NewLabel("Fyne System Tray"))
	w.SetCloseIntercept(func() {
		w.Hide()
	})

	a.Run()
}

type login struct {
	Url     string `json:"login_url"`
	Message string `json:"message"`
}

func doLogin(apiURL string) (string, error) {
	loginUrl, err := requestForUrl(apiURL)
	if err != nil {
		return "", err
	}

	if !isValidURL(loginUrl) {
		return "", fmt.Errorf("login url in wrong format or it's missing, url='%v'", loginUrl)
	}

	log.Debugf("waiting (3m) for response at %s/callback", pb.ClientLoginCallbackAddress)
	tokenCh := make(chan string)
	http.HandleFunc("/callback", func(rw http.ResponseWriter, req *http.Request) {
		log.Debugf("callback: %v %v %v %v %v", req.Method, req.URL.Path, req.Proto, req.ContentLength, req.Host)
		log.Debugf("callback headers: %v", req.Header)
		defer close(tokenCh)
		errQuery := req.URL.Query().Get("error")
		accessToken := req.URL.Query().Get("token")

		if errQuery != "" {
			msg := fmt.Sprintf("Login failed: %s", errQuery)
			_, _ = io.WriteString(rw, msg)
			fmt.Println(msg)
			tokenCh <- ""
			return
		}
		_, _ = io.WriteString(rw, static.LoginHTML)
		tokenCh <- accessToken
	})

	callbackHttpServer := http.Server{
		Addr: pb.ClientLoginCallbackAddress,
	}
	go callbackHttpServer.ListenAndServe()
	log.Debugf("trying opening browser with url=%v", loginUrl)
	if err := openBrowser(loginUrl); err != nil {
		fmt.Printf("Browser failed to open. \nPlease click on the link below:\n\n%s\n\n", loginUrl)
	}
	defer callbackHttpServer.Shutdown(context.Background())
	select {
	case accessToken := <-tokenCh:
		if accessToken == "" {
			return "", fmt.Errorf("empty token")
		}
		return accessToken, nil
	case <-time.After(3 * time.Minute):
		return "", fmt.Errorf("timeout (3m) on login")
	}
}

func requestForUrl(apiUrl string) (string, error) {
	c := http.DefaultClient
	url := fmt.Sprintf("%s/api/login", apiUrl)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	log.Debugf("GET %s/api/login status=%v", apiUrl, resp.StatusCode)
	defer resp.Body.Close()
	var l login
	if err := json.NewDecoder(resp.Body).Decode(&l); err != nil {
		return "", fmt.Errorf("failed decoding response body, err=%v", err)
	}
	if resp.StatusCode == http.StatusOK {
		return l.Url, nil
	}
	return "", fmt.Errorf("failed authenticating, status=%v, response=%v", resp.StatusCode, l.Message)
}

func fetchGrpcURL(apiURL, bearerToken string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/serverinfo", apiURL), nil)
	if err != nil {
		return "", err
	}
	req.Header.Add("authorization", fmt.Sprintf("Bearer %s", bearerToken))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	log.Debugf("GET %s/api/serverinfo status=%v", apiURL, resp.StatusCode)
	switch resp.StatusCode {
	case http.StatusOK:
		defer resp.Body.Close()
		serverInfo := map[string]any{}
		if err := json.NewDecoder(resp.Body).Decode(&serverInfo); err != nil {
			return "", fmt.Errorf("failed decoding response body, err=%v", err)
		}
		obj, ok := serverInfo["grpc_url"]
		if !ok {
			return "", fmt.Errorf("grpc_url parameter not present")
		}
		grpcURL, _ := obj.(string)
		if u, err := url.Parse(grpcURL); err != nil || u == nil {
			return "", fmt.Errorf("grpc_url parameter (%#v) is not a valid url, err=%v", obj, err)
		}
		return grpcURL, nil
	case http.StatusNotFound:
		return "", fmt.Errorf("the gateway does not have the serverinfo route")
	default:
		return "", fmt.Errorf("failed obtaining grpc url, status=%v", resp.StatusCode)
	}
}

func openBrowser(url string) (err error) {
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	return
}

func isValidURL(addr string) bool {
	u, err := url.Parse(addr)
	return err == nil && u.Scheme != "" && u.Host != ""
}

func printErrorAndExit(format string, v ...any) {
	fmt.Println(fmt.Sprintf(format, v...))
	os.Exit(1)
}
