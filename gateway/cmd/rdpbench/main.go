// Command rdpbench benchmarks the RDP PII detection pipeline against a real
// session recording.
//
// It replays the dirty-rect bitmap events stored in the session blob stream
// through the same composite -> snapshot -> OCR -> Presidio pipeline used by
// the gateway, and reports per-stage latencies. The goal is to measure how
// close to realtime each detector variant can run before wiring it into the
// live RDP path.
//
// Workflow:
//
//	# 1. Export a recorded RDP session from the database into a fixture file
//	rdpbench fetch -session <session-uuid> -o recording.json
//
//	# 2. Replay it as fast as possible (compare OCR engines / params)
//	rdpbench run -i recording.json
//
//	# 3. Replay it paced by the original timestamps (realtime simulation,
//	#    reports detection lag as if a kill switch were attached)
//	rdpbench run -i recording.json -pace realtime
//
// The fetch subcommand reads POSTGRES_DB_URI (or -db). The run subcommand
// requires a Presidio analyzer (MSPRESIDIO_ANALYZER_URL or -presidio) and the
// tesseract binary in PATH.
package main

import (
	"fmt"
	"os"
)

func usage() {
	fmt.Fprintf(os.Stderr, `usage: rdpbench <command> [flags]

commands:
  fetch     export a session recording from the database into a fixture file
  run       replay a fixture through the PII detection pipeline and report stats
  ocrbench  measure per-band-state OCR latency for an engine (tesseract or
            the HTTP PoC server in scripts/dev/ocr-poc)

Run 'rdpbench <command> -h' for command flags.
`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "fetch":
		err = runFetch(os.Args[2:])
	case "run":
		err = runBench(os.Args[2:])
	case "ocrbench":
		err = runOCRBench(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "rdpbench: unknown command %q\n", os.Args[1])
		usage()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "rdpbench: %v\n", err)
		os.Exit(1)
	}
}
