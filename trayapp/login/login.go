package login

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/runopsio/hoop/client/cmd/static"
)

var tokenCh = make(chan string)

func init() {
	http.HandleFunc("/callback", func(rw http.ResponseWriter, req *http.Request) {
		// defer close(tokenCh)
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

	callbackHttpServer := http.Server{Addr: "127.0.0.1:3587"}
	go callbackHttpServer.ListenAndServe()
}

func getRedirectURL(apiURL string) (string, error) {
	c := http.DefaultClient
	url := fmt.Sprintf("%s/api/login", apiURL)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("failed decoding response body, err=%v", err)
	}
	if resp.StatusCode == http.StatusOK {
		return out["login_url"], nil
	}
	return "", fmt.Errorf("failed authenticating, status=%v, response=%v", resp.StatusCode, out)
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

func Authenticate(apiURL string) (string, error) {
	redirectURL, err := getRedirectURL(apiURL)
	if err != nil {
		return "", err
	}
	if err := openBrowser(redirectURL); err != nil {
		return "", err
	}
	// defer callbackHttpServer.Shutdown(context.Background())
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Minute*3)
	defer cancelFn()
	select {
	case accessToken := <-tokenCh:
		if accessToken == "" {
			return "", fmt.Errorf("empty token")
		}
		return accessToken, nil
	case <-ctx.Done():
		return "", fmt.Errorf("timeout (3m) on login")
	}
}
