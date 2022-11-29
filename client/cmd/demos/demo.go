package demos

import (
	"fmt"
	"os"

	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
)

const (
	k8sConnectionName  = "k8s-demo"
	bashConnectionName = "bash-demo"
)

var DemoCmd = &cobra.Command{
	Use:          "demo",
	Short:        "Demo applications",
	SilenceUsage: false,
}

func init() {
	DemoCmd.AddCommand(bashCmd)
	DemoCmd.AddCommand(k8sCmd)
}

func printErrorAndExit(format string, v ...any) {
	p := termenv.ColorProfile()
	out := termenv.String(fmt.Sprintf(format, v...)).
		Foreground(p.Color("0")).
		Background(p.Color("#DBAB79"))
	fmt.Print(out.String())
	os.Exit(1)
}
