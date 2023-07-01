package main

import (
	"C"
)
import (
	"github.com/runopsio/hoop/agent"
)

//export __start_in_background
func __start_in_background() { go agent.StartSDK() }

//export __start
func __start() { agent.StartSDK() }

func main() {}
