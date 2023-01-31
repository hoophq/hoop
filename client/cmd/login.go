package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/runopsio/hoop/common/monitoring"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/spf13/cobra"
)

type (
	login struct {
		Url string `json:"login_url"`
	}
)

const (
	httpsProtocol = "https://"
)

var loginCmd = &cobra.Command{
	Use:    "login",
	Short:  "Authenticate at Hoop",
	Long:   `Login to gain access to hoop usage.`,
	PreRun: monitoring.SentryPreRun,
	Run: func(cmd *cobra.Command, args []string) {
		config, err := loadConfig()
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		if config.ApiURL == "" {
			reader := bufio.NewReader(os.Stdin)
			fmt.Print("Press enter to leave the defaults\n")
			fmt.Printf("API_URL [%s]: ", defaultApiURL)
			apiURL, _ := reader.ReadString('\n')
			apiURL = strings.Trim(apiURL, " \n")
			if apiURL == "" {
				apiURL = defaultApiURL
			}
			config.ApiURL = apiURL

			fmt.Printf("GRPC_PORT [%s]: ", defaultGrpcPort)
			grpcPort, _ := reader.ReadString('\n')
			grpcPort = strings.Trim(grpcPort, " \n")
			if grpcPort == "" {
				grpcPort = defaultGrpcPort
			}
			config.GrpcPort = grpcPort
			if err := saveConfig(config); err != nil {
				fmt.Println(err.Error())
				os.Exit(1)
			}
		}
		accessToken, err := doLogin(config.ApiURL)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		config.Token = accessToken
		if err := saveConfig(config); err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}

func doLogin(apiURL string) (string, error) {
	loginUrl, err := requestForUrl(apiURL)
	if err != nil {
		return "", err
	}

	if loginUrl == "" {
		return "", errors.New("missing login url")
	}

	tokenCh := make(chan string)
	http.HandleFunc("/callback", func(rw http.ResponseWriter, req *http.Request) {
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
		_, _ = io.WriteString(rw, "Login succeeded. You can close this tab now.")
		fmt.Println("Login succeeded")
		tokenCh <- accessToken
	})

	callbackHttpServer := http.Server{
		Addr: pb.ClientLoginCallbackAddress,
	}
	go callbackHttpServer.ListenAndServe()
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
	case <-time.After(30 * time.Second):
		return "", fmt.Errorf("timeout on login")
	}
}

func requestForUrl(apiUrl string) (string, error) {
	c := http.DefaultClient
	url := fmt.Sprintf("%s/api/login", apiUrl)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("accept", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}

		var l login
		if err := json.Unmarshal(b, &l); err != nil {
			return "", err
		}

		return l.Url, nil
	}

	return "", fmt.Errorf("failed authenticating, code=%v", resp.StatusCode)
}

func openBrowser(url string) error {
	var err error

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

	return err
}
