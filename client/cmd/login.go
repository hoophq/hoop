package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

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
		if err := doLogin(args); err != nil {
			fmt.Printf("Failed to login, err=%v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
	done = make(chan bool)
}

var done chan bool

func doLogin(args []string) error {
	defaultHost := "app.hoop.dev"
	defaultPort := "8443"

	config := loadConfig()

	if config.Host == "" {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Press enter to leave the defaults\n")
		fmt.Printf("Host [%s]: ", defaultHost)
		host, _ := reader.ReadString('\n')
		host = strings.Trim(host, " \n")

		if host == "" {
			host = defaultHost
		}

		host = strings.Replace(host, httpsProtocol, "", -1)
		config.Host = host
	}

	if config.Port == "" {
		reader := bufio.NewReader(os.Stdin)
		fmt.Printf("Port [%s]: ", defaultPort)
		port, _ := reader.ReadString('\n')
		port = strings.Trim(port, " \n")
		if port == "" {
			port = defaultPort
		}
		config.Port = port
	}

	saveConfig(config)

	loginUrl, err := requestForUrl(httpsProtocol + config.Host)
	if err != nil {
		return err
	}

	if loginUrl == "" {
		return errors.New("missing login url")
	}

	http.HandleFunc("/callback", loginCallback)
	go http.ListenAndServe(pb.ClientLoginCallbackAddress, nil)
	if err := openBrowser(loginUrl); err != nil {
		fmt.Printf("Browser failed to open. \nPlease click on the link below:\n\n%s\n\n", loginUrl)
	}

	<-done

	return nil
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

func loginCallback(resp http.ResponseWriter, req *http.Request) {
	err := req.URL.Query().Get("error")
	token := req.URL.Query().Get("token")

	browserMsg := "Login succeeded. You can close this tab now."
	userMsg := "Login succeeded\n"

	if err != "" {
		browserMsg = fmt.Sprintf("Login failed: %s", err)
		userMsg = fmt.Sprintf("Login failed: %s\n", err)

	}

	persistToken(token)

	io.WriteString(resp, browserMsg)
	fmt.Println(userMsg)

	done <- true
}

func persistToken(token string) {
	config := loadConfig()
	config.Token = token

	saveConfig(config)
}
