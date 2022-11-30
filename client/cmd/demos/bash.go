package demos

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"

	"github.com/runopsio/hoop/client/cmd/styles"
	"github.com/spf13/cobra"
)

var bashCmd = &cobra.Command{
	Use:          "bash",
	Short:        "Connect to an interactive bash session",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		runBashDemo()
	},
}

var connectionPayload = fmt.Sprintf(`
{
	"name": "%s",
	"agent_id": "test-agent",
	"type": "command-line",
	"command": [
	  "/bin/bash"
	],
	"secret": {}
}`, defaultBashConnectionName)

func runBashDemo() {
	req, err := http.NewRequest(
		"POST",
		"http://127.0.0.1:8009/api/connections",
		bytes.NewBufferString(connectionPayload))
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
	c := exec.Command("hoop", "connect", defaultBashConnectionName)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	_ = c.Run()
	fmt.Println("---")
	fmt.Println()
	fmt.Println(styles.Fainted.Render("  • Check the audit logs"))
	fmt.Println("  " + styles.Keyword(" http://127.0.0.1:8009/plugins/audit "))
	fmt.Println()
	fmt.Println(styles.Fainted.Render("  • You can now run the command in another terminal"))
	fmt.Printf("  $ hoop connect %s\n", defaultBashConnectionName)
	fmt.Println()
}
