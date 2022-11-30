package demos

import (
	"fmt"
	"os"

	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
)

const (
	defaultK8sConnectionName  = "k8s-demo"
	defaultBashConnectionName = "bash-demo"
)

var DemoCmd = &cobra.Command{
	Use:          "demo",
	Short:        "Demo applications",
	SilenceUsage: false,
}

var connectionNameFlag string

func init() {
	DemoCmd.AddCommand(bashCmd)
	DemoCmd.AddCommand(k8sCmd)
}

func printErrorAndExit(format string, v ...any) {
	p := termenv.ColorProfile()
	out := termenv.String(fmt.Sprintf(format, v...)).
		Foreground(p.Color("0")).
		Background(p.Color("#DBAB79"))
	fmt.Println(out.String())
	os.Exit(1)
}
