package demos

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os/exec"

	"github.com/runopsio/hoop/client/cmd/styles"
	"github.com/spf13/cobra"
)

var k8sCmd = &cobra.Command{
	Use:          "k8s",
	Short:        "Execute a command in a Kubernetes cluster",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		runK8sDemo()
	},
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

func runK8sDemo() {
	// TODO: check if connection is created before boostraping the token
	c := exec.Command("hoop", "bootstrap", "k8s", "token-granter")
	output, err := c.CombinedOutput()
	if err != nil {
		printErrorAndExit("failed bootstraping k8s. err=%v, stdout=%v", err, string(output))
	}
	kubeconfigBase64Enc := base64.StdEncoding.EncodeToString(output)
	req, err := http.NewRequest(
		"POST",
		"http://127.0.0.1:8009/api/connections",
		bytes.NewBufferString(fmt.Sprintf(
			k8sConnectionPayloadTemplate,
			k8sConnectionName,
			kubeconfigBase64Enc)))
	if err != nil {
		printErrorAndExit(err.Error())
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		printErrorAndExit(err.Error())
	}
	defer resp.Body.Close()
	respErr, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		printErrorAndExit("status-code=%v, resp-err=%v", resp.Status, string(respErr))
	}

	c = exec.Command("hoop", "exec", k8sConnectionName, "--", "get", "namespaces")
	output, err = c.CombinedOutput()
	if err != nil {
		printErrorAndExit("failed executing demo. err=%v, stdout=%v", err, string(output))
	}
	fmt.Println(string(output))
	fmt.Println("---")
	fmt.Println()
	fmt.Println(styles.Fainted.Render("  • You can now run the command in another terminal"))
	fmt.Printf("  $ hoop exec %s -- get namespaces\n", k8sConnectionName)
	fmt.Println()
}
