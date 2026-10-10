//go:build !tinygo

package codec

import (
	"fmt"
	"os"
)

// Off the relay there is no host. The imports that return data fail
// loudly, so a unit test that strays into one names the import instead of
// linking against nothing. Log goes to stderr, because a codec logs on
// paths a host-side test wants to reach (a refused Open).

func hostAnalyzeSQL(ptr, n uint32) uint64 { unavailable("analyze_sql"); return 0 }

func hostSplitSQL(ptr, n uint32) uint64 { unavailable("split_sql"); return 0 }

func hostMask(cptr, cn, vptr, vn uint32) uint64 { unavailable("mask"); return 0 }

func unavailable(name string) {
	panic("hoop-codec: host import hoop." + name + " is only available inside the relay (TinyGo wasm build)")
}

// Log writes one line to stderr.
func Log(level Level, message string) {
	fmt.Fprintf(os.Stderr, "hoop[%d] %s\n", level, message)
}
