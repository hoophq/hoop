package cmd

import (
	"fmt"
	"github.com/BurntSushi/toml"
	"log"
	"os"
)

type (
	Config struct {
		Token         string
		ServerAddress string
		Email         string
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

	filepath := fmt.Sprintf("%s/config.toml", path)
	if _, err := os.Stat(filepath); os.IsNotExist(err) {
		f, err := os.Create(filepath)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
	}

	return filepath
}
