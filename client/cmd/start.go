package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/runopsio/hoop/gateway"

	"github.com/briandowns/spinner"
	"github.com/runopsio/hoop/agent"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:          "start",
	Short:        "Runs hoop local demo",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		loader := spinner.New(spinner.CharSets[78], 70*time.Millisecond)
		loader.Color("green")
		loader.Start()
		defer loader.Stop()
		loader.Suffix = " starting hoop -> downloading docker image ..."
		imageName := "hoophq/hoop"
		containerName := "hoopdemo"

		done := make(chan os.Signal, 1)
		signal.Notify(done, syscall.SIGTERM, syscall.SIGINT)
		// TODO: make resize to propagate remotely!
		go func() {
			for {
				switch <-done {
				case syscall.SIGTERM, syscall.SIGINT:
					loader.Stop() // this fixes terminal restore
					os.Exit(1)
				}
			}
		}()
		// Cleanup signals when done.
		defer func() { signal.Stop(done); close(done) }()

		_ = exec.Command("docker", "stop", containerName).Run()
		_ = exec.Command("docker", "rm", containerName).Run()
		if stdout, err := exec.Command("docker", "pull", imageName).CombinedOutput(); err != nil {
			fmt.Printf("failed pulling image %v, err=%v, stdout=%v\n", imageName, err, string(stdout))
			os.Exit(1)
		}
		dockerArgs := []string{
			"run",
			"-e", "PROFILE=dev",
			"-p", "8009:8009",
			"-p", "8010:8010",
			"--name", containerName,
			"-d", imageName,
		}

		execmd := exec.Command("docker", dockerArgs...)
		cmdres, err := execmd.CombinedOutput()
		if err != nil {
			fmt.Printf("output=%v, err=%v\n", string(cmdres), err)
			os.Exit(1)
		}
		ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*45)
		defer cancelFn()
		loader.Suffix = " starting hoop -> running container ..."
		go func() {
			index := 0
			for {
				select {
				case <-ctx.Done():
					data, _ := exec.Command("docker", "logs", containerName).
						CombinedOutput()
					if len(data) > 0 {
						loader.Stop()
						fmt.Println("failed starting hoop (timeout)! docker logs")
						fmt.Println("---")
						fmt.Println("---")
						fmt.Println(string(data))
					}
					os.Exit(1)
				default:
					_, err := http.Get("http://127.0.0.1:8009")
					if err == nil {
						loader.Stop()
						fmt.Println("-> hoop started!")
						fmt.Println("open http://127.0.0.1:8009 to begin")
						fmt.Println("")
						fmt.Println("stop the demo")
						fmt.Println("docker stop hoopdemo")
						fmt.Println("")
						os.Exit(0)
					}
					index++
					if index == 10 {
						loader.Suffix = " starting hoop -> still starting, hang it there ..."
					}
					time.Sleep(time.Second * 1)
				}
			}
		}()
		<-done
	},
}

var startAgentCmd = &cobra.Command{
	Use:          "agent",
	Short:        "Runs the agent component",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		agent.Run()
	},
}

var startGatewayCmd = &cobra.Command{
	Use:          "gateway",
	Short:        "Runs the gateway component",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		gateway.Run()
	},
}

func init() {
	startCmd.AddCommand(startAgentCmd)
	startCmd.AddCommand(startGatewayCmd)
	rootCmd.AddCommand(startCmd)
}
