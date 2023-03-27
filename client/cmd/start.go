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

	"github.com/briandowns/spinner"
	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/agent"
	"github.com/runopsio/hoop/client/cmd/demos"
	"github.com/runopsio/hoop/client/cmd/styles"
	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/monitoring"
	"github.com/runopsio/hoop/gateway"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:          "start",
	Short:        "Runs hoop local demo",
	SilenceUsage: false,
	PreRun:       monitoring.SentryPreRun,
	Run: func(cmd *cobra.Command, args []string) {
		loader := spinner.New(spinner.CharSets[11], 70*time.Millisecond)
		loader.Color("green")
		loader.Start()
		defer loader.Stop()
		loader.Suffix = " starting hoop -> downloading docker image ..."
		imageName := "hoophq/hoop"
		containerName := "hoopdemo"

		done := make(chan os.Signal, 1)
		signal.Notify(done, syscall.SIGTERM, syscall.SIGINT)
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
			sentry.CaptureException(fmt.Errorf("start-app - failed pulling image, stdout=%v, err=%v", string(stdout), err))
			fmt.Printf("failed pulling image %v, err=%v, stdout=%v\n", imageName, err, string(stdout))
			os.Exit(1)
		}
		dockerArgs := []string{
			"run",
			"-t", // required for resizing the tty in the agent properly
			"-e", "LOG_LEVEL=" + log.LevelDebug,
			"-e", fmt.Sprintf("PYROSCOPE_INGEST_URL=%v", os.Getenv("PYROSCOPE_INGEST_URL")),
			"-e", fmt.Sprintf("PYROSCOPE_AUTH_TOKEN=%v", os.Getenv("PYROSCOPE_AUTH_TOKEN")),
			"-e", fmt.Sprintf("AGENT_SENTRY_DSN=%v", os.Getenv("AGENT_SENTRY_DSN")),
			"-e", fmt.Sprintf("API_URL=%v", os.Getenv("API_URL")),
			"-e", fmt.Sprintf("IDP_ISSUER=%v", os.Getenv("IDP_ISSUER")),
			"-e", fmt.Sprintf("IDP_CLIENT_ID=%v", os.Getenv("IDP_CLIENT_ID")),
			"-e", fmt.Sprintf("IDP_CLIENT_SECRET=%v", os.Getenv("IDP_CLIENT_SECRET")),
			"-e", fmt.Sprintf("IDP_AUDIENCE=%v", os.Getenv("IDP_AUDIENCE")),
			"-e", fmt.Sprintf("GOOGLE_APPLICATION_CREDENTIALS_JSON=%v",
				os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON")),
		}
		dockerArgs = append(dockerArgs,
			"-p", "8009:8009",
			"-p", "8010:8010",
			"--name", containerName,
			"-d", imageName,
		)

		var hasAuth bool
		if os.Getenv("IDP_ISSUER") == "" {
			dockerArgs = append(dockerArgs, "/app/start-dev.sh")
		} else {
			dockerArgs = append(dockerArgs, "/app/start-idp-dev.sh")
			hasAuth = true
		}

		execmd := exec.Command("docker", dockerArgs...)
		cmdres, err := execmd.CombinedOutput()
		if err != nil {
			sentry.CaptureException(fmt.Errorf("start-app - failed starting hoop locally, output=%v, err=%v", string(cmdres), err))
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
					sentry.CaptureException(fmt.Errorf("start-app - failed starting hoop (timeout), logs=%v", string(data)))
					if len(data) > 0 {
						loader.Stop()
						fmt.Println("failed starting hoop (timeout)! docker logs")
						fmt.Println("---")
						fmt.Println("---")
						fmt.Println(string(data))
					}
					os.Exit(1)
				default:
					resp, err := http.Get("http://127.0.0.1:8009/api/agents")
					if err == nil && resp.StatusCode == 200 {
						loader.Stop()
						fmt.Println()
						fmt.Println(styles.Default.Render("  hoop started at " + styles.Keyword(" http://127.0.0.1:8009 ")))
						fmt.Println()
						if hasAuth {
							renderAuthDemo()
						} else {
							renderNonAuthDemo()
						}
						// best-effort to rename the config file when starting the demo.
						// this fixes errors when trying the demo if the user has logged in before.
						renameClientConfigs()
						os.Exit(0)
					}
					index++
					if index == 20 {
						loader.Suffix = " starting hoop -> still starting, hang it there ..."
					}
					time.Sleep(time.Millisecond * 500)
				}
			}
		}()
		<-done
	},
}

func renderAuthDemo() {
	fmt.Println(styles.Fainted.Render("  • Start an agent"))
	fmt.Println(styles.Default.Render("  $ hoop start agent"))
	fmt.Println()
	fmt.Println(styles.Fainted.Render("  • Stop the demo"))
	fmt.Println(styles.Default.Render("  $ docker stop hoopdemo"))
	fmt.Println()
}

func renderNonAuthDemo() {
	fmt.Println(styles.Fainted.Render("  • Connect to an interactive audited bash session"))
	fmt.Println(styles.Default.Render("  $ hoop start demo bash"))
	fmt.Println()
	fmt.Println(styles.Fainted.Render("  • Access Kubernetes resources"))
	fmt.Println(styles.Default.Render("  $ hoop start demo k8s"))
	fmt.Println()
	fmt.Println(styles.Fainted.Render("  • Stop the demo"))
	fmt.Println(styles.Default.Render("  $ docker stop hoopdemo"))
	fmt.Println()
}

func renameClientConfigs() {
	if filepath, err := clientconfig.NewPath(clientconfig.AgentFile); err == nil {
		if fi, _ := os.Stat(filepath); fi != nil && fi.Size() > 0 {
			_ = os.Rename(filepath, fmt.Sprintf("%s-bkp", filepath))
		}
	}
	if filepath, err := clientconfig.NewPath(clientconfig.ClientFile); err == nil {
		if fi, _ := os.Stat(filepath); fi != nil && fi.Size() > 0 {
			_ = os.Rename(filepath, fmt.Sprintf("%s-bkp", filepath))
		}
	}
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
	startCmd.AddCommand(demos.DemoCmd)
	rootCmd.AddCommand(startCmd)
}
