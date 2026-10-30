// Command upstream serves the ENG-987 test MCP server.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/hoophq/hoop/sidecar/mcpgateway/internal/testupstream"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:18900", "address")
	flag.Parse()
	log.Printf("test MCP upstream on http://%s/mcp and http://%s/sse", *addr, *addr)
	log.Fatal(http.ListenAndServe(*addr, testupstream.Handler(testupstream.NewServer(""))))
}
