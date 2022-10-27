package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	pb "github.com/runopsio/hoop/common/proto"
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
	config := loadConfig()

	if config.Host == "" {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("No server address configured.\n")
		fmt.Printf("Server address [https://%s]: ", pb.DefaultHost)
		host, _ := reader.ReadString('\n')
		host = strings.Trim(config.Email, " \n")
		if host == "" {
			host = "https://" + pb.DefaultHost
		}
		config.Host = host
	}

	if !strings.HasPrefix(config.Host, "https://") {
		config.Host = "https://" + config.Host
	}

	if len(args) > 0 {
		config.Email = args[0]
	}

	if config.Email == "" {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Email: ")
		config.Email, _ = reader.ReadString('\n')
		config.Email = strings.Trim(config.Email, " \n")
	}

	saveConfig(config)

	fmt.Printf("To use a different email, please run 'hoop login {email}'.\n\nLogging with [%s] at [%s]\n\n.", config.Email, config.Host)

	loginUrl, err := requestForUrl(config.Email, config.Host)
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

	return "https://", nil
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

	persistTokenFilesystem(token)

	io.WriteString(resp, browserMsg)
	fmt.Println(userMsg)

	done <- true
}

func persistTokenFilesystem(token string) {
	config := loadConfig()
	config.Token = token

	saveConfig(config)
}
