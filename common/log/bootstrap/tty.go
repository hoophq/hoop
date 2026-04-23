package bootstrap

import (
	"os"

	"github.com/mattn/go-isatty"
)

// shouldUseTTY decides whether to render phased, colored output to stdout.
// LOG_ENCODING=human always opts in; any other non-empty value opts out.
// When unset, we fall back to auto-detecting whether stdout is a terminal.
func shouldUseTTY() bool {
	enc := os.Getenv("LOG_ENCODING")
	if enc == "human" {
		return true
	}
	if enc != "" {
		return false
	}
	return isatty.IsTerminal(os.Stdout.Fd())
}

// noColor honors the de-facto NO_COLOR convention (https://no-color.org).
// When set, the TTY renderer still groups output into phases but avoids
// ANSI escapes and swaps Unicode glyphs for ASCII equivalents.
func noColor() bool {
	return os.Getenv("NO_COLOR") != ""
}
