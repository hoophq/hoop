package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/agent/autoregister"
	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/version"
)

func Run() {
	fmt.Println(string(version.JSON()))
	defaultServerAddress := "127.0.0.1:8010"
	secondaryServerAddres := "app.hoop.dev:8443"
	agentToken, err := autoregister.Run()
	if err != nil {
		log.Fatal(err)
	}
	if agentToken != "" {
		saveConfig(&Config{ServerAddress: defaultServerAddress, Token: agentToken})
	}

	conf := loadConfig()
	if conf.Token == "" {
		conf.Token = os.Getenv("TOKEN")
		if conf.Token == "" {
			conf.Token = "x-agt-" + uuid.NewString()
		}
	}

	if conf.ServerAddress == "" {
		conf.ServerAddress = os.Getenv("SERVER_ADDRESS")
		if conf.ServerAddress == "" {
			conf.ServerAddress = defaultServerAddress
		}
	}
	log.Debugf("server=%v, token-length=%v", conf.ServerAddress, len(conf.Token))
	client, err := grpc.Connect(conf.ServerAddress, conf.Token, grpc.WithOption("origin", pb.ConnectionOriginAgent))
	if err != nil {
		if strings.HasPrefix(conf.ServerAddress, defaultServerAddress) {
			conf.ServerAddress = secondaryServerAddres
			fmt.Printf("Trying remote server...")
			client, err = grpc.Connect(conf.ServerAddress, conf.Token, grpc.WithOption("origin", pb.ConnectionOriginAgent))
			if err != nil {
				log.Printf("disconnecting, err=%v", err.Error())
				os.Exit(1)
			}
		} else {
			log.Printf("failed to connect to [%s], err=%v", conf.ServerAddress, err.Error())
			os.Exit(1)
		}
	}

	saveConfig(conf)

	firstTry := true
	for i := 1; i < 100; i++ {
		ctx := client.StreamContext()
		agt := New(client)

		if err := runWithError(ctx, conf, agt, firstTry); err != nil {
			time.Sleep(time.Second * 5)
			fmt.Print(".")
			firstTry = false
			client, err = grpc.Connect(conf.ServerAddress, conf.Token, grpc.WithOption("origin", pb.ConnectionOriginAgent))
			if err != nil {
				log.Printf("failed to connect to [%s], err=%v", conf.ServerAddress, err.Error())
				os.Exit(1)
			}
			continue
		}
		break
	}

	log.Println("server terminated connection... exiting...")
}

func runWithError(ctx context.Context, conf *Config, agt *Agent, firstTry bool) error {
	go agt.Run(conf.ServerAddress, conf.Token, firstTry)
	<-ctx.Done()
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

type (
	Config struct {
		Token         string
		ServerAddress string
	}
)

func loadConfig() *Config {
	path := getFilepath()
	var conf Config
	if _, err := toml.DecodeFile(path, &conf); err != nil {
		panic(err)
	}

	return &conf
}

func saveConfig(conf *Config) {
	f, err := os.OpenFile(getFilepath(), os.O_WRONLY, os.ModeAppend)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	if err := f.Truncate(0); err != nil {
		panic(err)
	}

	f.Seek(0, 0)

	if err := toml.NewEncoder(f).Encode(conf); err != nil {
		panic(err)
	}
}

func getFilepath() string {
	filepath, err := clientconfig.NewPath(clientconfig.AgentFile)
	if err != nil {
		panic(err)
	}
	return filepath
}
