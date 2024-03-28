package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/runopsio/hoop/client/cmd/static"
	proxyconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/monitoring"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/spf13/cobra"
)

type login struct {
	Url     string `json:"login_url"`
	Message string `json:"message"`
}

var loginCmd = &cobra.Command{
	Use:    "login",
	Short:  "Authenticate at Hoop",
	Long:   `Login to gain access to hoop usage.`,
	PreRun: monitoring.SentryPreRun,
	Run: func(cmd *cobra.Command, args []string) {
		conf, err := proxyconfig.Load()
		switch err {
		case proxyconfig.ErrEmpty:
			configureHostsPrompt(conf)
		case nil:
			// if the configuration was edited manually
			// validate it and prompt for a new one if it's not valid
			if !conf.IsValid() {
				configureHostsPrompt(conf)
			}
		default:
			printErrorAndExit(err.Error())
		}
		log.Debugf("loaded configuration file, mode=%v, grpc_url=%v, api_url=%v, tokenlength=%v",
			conf.Mode, conf.GrpcURL, conf.ApiURL, len(conf.Token))
		// perform the login and save the token
		conf.Token, err = doLogin(conf.ApiURL)
		if err != nil {
			printErrorAndExit(err.Error())
		}
		if conf.GrpcURL == "" {
			// best-effort to obtain the obtain the grpc url
			// if it's not set
			conf.GrpcURL, err = fetchGrpcURL(conf.ApiURL, conf.Token)
			if err != nil {
				printErrorAndExit(err.Error())
			}
			log.Debugf("obtained remote grpc url %v", conf.GrpcURL)
		}
		log.Debugf("saving token, length=%v", len(conf.Token))
		saved, err := conf.Save()
		if err != nil {
			printErrorAndExit(err.Error())
		}
		if saved {
			fmt.Println("Login succeeded")
		} else {
			// means it's a local gateway (development)
			// print to stdout
			fmt.Println(conf.Token)
		}
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}

func configureHostsPrompt(conf *proxyconfig.Config) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Press enter to leave the defaults\n")
	fmt.Printf("API_URL [%s]: ", clientconfig.SaaSWebURL)
	apiURL, _ := reader.ReadString('\n')
	apiURL = strings.Trim(apiURL, " \n")
	apiURL = strings.TrimSpace(apiURL)
	if apiURL == "" {
		apiURL = clientconfig.SaaSWebURL
	}
	conf.ApiURL = apiURL
	if _, err := conf.Save(); err != nil {
		printErrorAndExit(err.Error())
	}
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
