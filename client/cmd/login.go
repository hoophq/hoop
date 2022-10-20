package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/spf13/cobra"
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

// loginCmd represents the login command
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
}

func doLogin(args []string) error {
	var email string
	if len(args) == 0 {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Email > ")
		email, _ = reader.ReadString('\n')
		email = strings.Trim(email, " \n")
	} else {
		email = args[0]
	}

	apiUrl := os.Getenv("API_URL")
	if apiUrl == "" {
		apiUrl = pb.DefaultHost
	}

	loginUrl, err := requestForUrl(email, apiUrl)
	if err != nil {
		return err
	}

	if loginUrl == "" {
		return errors.New("missing login url")
	}

	// start server

	if err := openBrowser(loginUrl); err != nil {
		fmt.Printf("Browser failed to open. \nPlease click on the link below:\n\n%s\n\n", loginUrl)
	}

	// wait callback

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
