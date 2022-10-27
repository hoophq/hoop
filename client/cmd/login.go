package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
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
	Use:   "login",
	Short: "Authenticate at Hoop",
	Long:  `Login to gain access to hoop usage.`,
	Run: func(cmd *cobra.Command, args []string) {
		if err := doLogin(args); err != nil {
			fmt.Println("Failed to login. Please try again")
			os.Exit(1)
		}
		os.Exit(0)
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().BoolP("email", "u", false, "The email used to authenticate at hoop")
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

	if len(args) > 0 {
		config.Email = args[0]
	}

	if config.Email == "" {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Email: ")
		email, _ := reader.ReadString('\n')
		email = strings.Trim(email, " \n")
		config.Email = email
	}

	saveConfig(config)

	fmt.Printf("To use a different email, please run 'hoop login {email}'.\n\nLogging with [%s] at [%s]\n\n", config.Email, config.Host)

	loginUrl, err := requestForUrl(config.Email, httpsProtocol+config.Host)
	if err != nil {
		return err
	}

	if loginUrl == "" {
		return errors.New("missing login url")
	}

	http.HandleFunc("/callback", loginCallback)
	go http.ListenAndServe(":3333", nil)

	if err := openBrowser(loginUrl); err != nil {
		fmt.Printf("Browser failed to open. \nPlease click on the link below:\n\n%s\n\n", loginUrl)
	}

	<-done

	return nil
}

func requestForUrl(email, apiUrl string) (string, error) {
	c := http.DefaultClient
	url := fmt.Sprintf("%s/api/login", apiUrl)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("accept", "application/json")

	q := req.URL.Query()
	q.Add("email", email)
	req.URL.RawQuery = q.Encode()

	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}

		var l login
		if err := json.Unmarshal(b, &l); err != nil {
			return "", nil
		}

		return l.Url, nil
	}

	return httpsProtocol, nil
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

	browserMsg := fmt.Sprintf("Login succeeded. You can close this tab now.")
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
