package agent

import (
	"context"
	"fmt"
	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/grpc"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/version"
	"log"
	"os"
	"time"
)

func Run() {
	fmt.Println(string(version.JSON()))

	defaultServerAddress := "127.0.0.1:8010"

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

	saveConfig(conf)

	var agt *Agent
	defer agt.Close()

	firstTry := true
	for i := 1; i < 100; i++ {
		client, err := connect(conf)
		if err != nil {
			log.Printf("disconnecting, msg=%v", err.Error())
			os.Exit(1)
		}

		ctx := client.StreamContext()
		done := make(chan struct{})
		agt = New(client, done)

		if err := runWithError(ctx, conf, agt, firstTry); err != nil {
			time.Sleep(time.Second * 5)
			fmt.Print(".")
			firstTry = false
			continue
		}
	}

	log.Println("Server terminated connection... exiting...")
}

func runWithError(ctx context.Context, conf *Config, agt *Agent, firstTry bool) error {
	go agt.Run(conf.ServerAddress, conf.Token, firstTry)
	<-ctx.Done()
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func connect(conf *Config) (pb.ClientTransport, error) {
	client, err := grpc.Connect(conf.ServerAddress, conf.Token, grpc.WithOption("origin", pb.ConnectionOriginAgent))
	if err != nil {
		return nil, err
	}

	return client, nil
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
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}

	path := fmt.Sprintf("%s/.hoop", home)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0700); err != nil {
			panic(err)
		}
	}

	filepath := fmt.Sprintf("%s/agent.toml", path)
	if _, err := os.Stat(filepath); os.IsNotExist(err) {
		f, err := os.Create(filepath)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
	}

	return filepath
}
