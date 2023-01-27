package demos

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os/exec"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/client/cmd/styles"
	"github.com/runopsio/hoop/client/k8s"
	"github.com/spf13/cobra"
)

var k8sCmd = &cobra.Command{
	Use:          "k8s",
	Short:        "Execute a command in a Kubernetes cluster",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		if err := runK8sDemo(); err != nil {
			sentry.CaptureException(fmt.Errorf("demo-k8s - %v", err))
			printErrorAndExit(err.Error())
		}
	},
}

func init() {
	k8sCmd.Flags().StringVarP(&connectionNameFlag, "connection", "c", defaultK8sConnectionName, "The name of the connection to create")
}

var k8sConnectionPayloadTemplate = `
{
	"name": "%s",
	"agent_id": "test-agent",
	"type": "command-line",
	"command": ["kubectl"],
	"secret": {
		"filesystem:KUBECONFIG": "%s"
	}
}`

func connectionExists(connectionName string) (bool, error) {
	resp, err := http.Get("http://127.0.0.1:8009/api/connections/" + connectionName)
	if err != nil {
		return false, err
	}
	switch resp.StatusCode {
	case 404:
		return false, nil
	case 200:
		return true, nil
	default:
		return false, fmt.Errorf("unknow status code (%v)", resp.StatusCode)
	}
}

func runK8sDemo() error {
	exists, err := connectionExists(connectionNameFlag)
	if exists {
		return fmt.Errorf("connection %s already exists, to use a distinct one: hoop start demo k8s -c <mynewconn>",
			connectionNameFlag)
	}
	if err != nil {
		return err
	}
	c := exec.Command("hoop", "bootstrap", "k8s", "token-granter")
	output, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed bootstraping k8s. err=%v, stdout=%v", err, string(output))
	}
	kubeconfigBase64Enc := base64.StdEncoding.EncodeToString(output)
	req, err := http.NewRequest(
		"POST",
		"http://127.0.0.1:8009/api/connections",
		bytes.NewBufferString(fmt.Sprintf(
			k8sConnectionPayloadTemplate,
			connectionNameFlag,
			kubeconfigBase64Enc)))
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respErr, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		sentry.CaptureException(fmt.Errorf("demo-k8s - failed creating connection, statuscode=%v, resperr=%v",
			resp.StatusCode, string(respErr)))
		return fmt.Errorf("status-code=%v, resp-err=%v", resp.StatusCode, string(respErr))
	}

	c = exec.Command("hoop", "exec", connectionNameFlag, "--", "get", "namespaces")
	output, err = c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed executing demo. err=%v, stdout=%v", err, string(output))
	}
	fmt.Println(string(output))
	fmt.Println("---")
	fmt.Println()
	fmt.Println(styles.Fainted.Render("  • You can now run the command in another terminal"))
	fmt.Printf("  $ hoop exec %s -- get namespaces\n", connectionNameFlag)
	fmt.Println()
	fmt.Println(styles.Fainted.Render("  • To clean up, delete the namespace"))
	fmt.Printf("  $ kubectl delete ns %s\n", k8s.DefaultNamespaceName)
	fmt.Println()
	return nil
}
