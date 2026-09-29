package proxy

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/hoophq/hoop/sidecar/session"
)

// StartupMetadata lifts one value out of a pgwire StartupMessage into the
// session's metadata, so every statement the session runs carries it on its
// audit record and in the OPA input as input.context.<Key>.
//
// It exists for end-to-end tracing. A client that knows which task or agent
// run it serves (PGOPTIONS='-c claude.session.id=...') can stamp that on the
// connection with nothing but standard libpq, and the trail can then answer
// "which run issued this statement" without a join to another system.
//
// The VALUE is the client's claim and is never verified; see startupParams.
// It is a label for correlation, not an identity, which is why it lands in
// Metadata and not in session.Identity. The KEY is the operator's, and Validate
// refuses one that would overwrite a field PolicyContext owns.
type StartupMetadata struct {
	// Option names a setting passed inside the `options` startup parameter,
	// written `-c name=value` or `--name=value` (libpq's PGOPTIONS, pgjdbc's
	// `options` property). It is matched the way Postgres matches a setting
	// name: case-insensitively, with '-' read as '_'.
	Option string

	// Parameter names a StartupMessage parameter, such as application_name,
	// matched exactly. Exclusive with Option.
	Parameter string

	// Key is the session metadata key the value is recorded under.
	Key string
}

// maxStartupMetadataValue bounds one lifted value, in bytes. The value is
// client-supplied and copied onto every statement record of the session, so
// an unbounded one multiplies into the trail. 256 bytes holds any id,
// UUID or URL-safe token a tracing system issues.
const maxStartupMetadataValue = 256

// Validate reports a field that cannot be applied.
func (f StartupMetadata) Validate() error {
	switch {
	case f.Option == "" && f.Parameter == "":
		return errors.New("startup metadata names neither an option nor a parameter")
	case f.Option != "" && f.Parameter != "":
		return fmt.Errorf("startup metadata names both option %q and parameter %q; keep one",
			f.Option, f.Parameter)
	case f.Key == "":
		return errors.New("startup metadata has no key")
	case session.ReservedContextKey(f.Key):
		// The value is the client's, so letting it land on a key the
		// policy context owns would let a client name its own principal.
		return fmt.Errorf("startup metadata key %q is reserved: input.context.%s is set by the relay",
			f.Key, f.Key)
	}
	if f.Option != "" && strings.ContainsAny(f.Option, "= \t\r\n\v\f\\\x00") {
		return fmt.Errorf("startup metadata option %q is not a setting name", f.Option)
	}
	if strings.ContainsRune(f.Parameter, 0) {
		return fmt.Errorf("startup metadata parameter %q contains a NUL", f.Parameter)
	}
	return nil
}

// validateStartupMetadata checks a lane's field list as one unit: each field
// on its own, and no two fields writing the same key, because the second
// would silently decide what the first recorded.
func validateStartupMetadata(fields []StartupMetadata) error {
	seen := make(map[string]bool, len(fields))
	for _, f := range fields {
		if err := f.Validate(); err != nil {
			return err
		}
		if seen[f.Key] {
			return fmt.Errorf("startup metadata key %q is written by two fields", f.Key)
		}
		seen[f.Key] = true
	}
	return nil
}

// startupMetadata resolves fields against the parameters a StartupMessage
// carried. A field whose source is absent records nothing: no key rather than
// an empty value, so a policy can tell "not sent" from "sent empty".
func startupMetadata(params map[string]string, fields []StartupMetadata) map[string]string {
	if len(params) == 0 || len(fields) == 0 {
		return nil
	}
	var (
		opts     map[string]string
		optsRead bool
		out      map[string]string
	)
	for _, f := range fields {
		var (
			v  string
			ok bool
		)
		if f.Parameter != "" {
			v, ok = params[f.Parameter]
		} else {
			if !optsRead {
				opts, optsRead = pgOptionSettings(params["options"]), true
			}
			v, ok = opts[pgSettingName(f.Option)]
		}
		if !ok {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(fields))
		}
		out[f.Key] = truncateUTF8(v, maxStartupMetadataValue)
	}
	return out
}

// pgOptionSettings returns the settings an `options` startup parameter
// assigns, keyed by normalized name, reading it the way the backend does so
// the value recorded is the value the session runs with.
//
// # The grammar being mirrored
//
// pg_split_opts (src/backend/utils/init/postinit.c) splits the string on
// whitespace, a backslash escaping the character after it. The words are then
// read by process_postgres_switches, which is getopt over the server's own
// switches, "B:bC:c:D:d:EeFf:h:ijk:lN:nOPp:r:S:sTt:v:W:-:". Only two of those
// assign a named setting: `-c name=value` and `--name=value`. Every other
// switch is skipped with its argument, because reading `-d 5` as the start of
// a new word would desynchronize every setting after it.
//
// Parsing stops where getopt stops: at the first word that is not a switch, at
// `--`, or at a switch the backend does not know. In each case the backend
// refuses the connection, so no statement can run under what follows and
// nothing after that point is worth reading.
func pgOptionSettings(options string) map[string]string {
	if options == "" {
		return nil
	}
	words := pgSplitOptions(options)
	out := make(map[string]string)
	for i := 0; i < len(words); i++ {
		w := words[i]
		if len(w) < 2 || w[0] != '-' || w == "--" {
			return out
		}
		// A cluster such as -Ec: letters that take no argument run on, and
		// the first one that does takes the rest of the word or, when that is
		// empty, the next word.
		for j := 1; j < len(w); j++ {
			letter := w[j]
			takesArg, known := pgSwitchArg(letter)
			if !known {
				return out
			}
			if !takesArg {
				continue
			}
			arg := w[j+1:]
			if arg == "" {
				if i+1 >= len(words) {
					return out
				}
				i++
				arg = words[i]
			}
			if letter == 'c' || letter == '-' {
				// ParseLongOption: no '=' is "requires a value", which the
				// backend refuses, so there is nothing to record.
				name, value, ok := strings.Cut(arg, "=")
				if !ok {
					return out
				}
				out[pgSettingName(name)] = value
			}
			break
		}
	}
	return out
}

// pgSwitchArg reports whether a backend switch letter takes an argument, and
// whether the backend knows the letter at all. It is the optstring
// process_postgres_switches hands getopt.
func pgSwitchArg(letter byte) (takesArg, known bool) {
	switch letter {
	case 'B', 'C', 'c', 'D', 'd', 'f', 'h', 'k', 'N', 'p', 'r', 'S', 't', 'v', 'W', '-':
		return true, true
	case 'b', 'E', 'e', 'F', 'i', 'j', 'l', 'n', 'O', 'P', 's', 'T':
		return false, true
	}
	return false, false
}

// pgSplitOptions is pg_split_opts: words separated by C isspace, a backslash
// taking the next character literally, including another backslash or a
// space.
func pgSplitOptions(s string) []string {
	var (
		words   []string
		word    strings.Builder
		inWord  bool
		escaped bool
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !escaped && pgIsSpace(c) {
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
			continue
		}
		inWord = true
		if !escaped && c == '\\' {
			escaped = true
			continue
		}
		escaped = false
		word.WriteByte(c)
	}
	if inWord {
		words = append(words, word.String())
	}
	return words
}

// pgIsSpace is C isspace in the "C" locale, which is what the backend runs
// pg_split_opts under before any locale is loaded.
func pgIsSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

// pgSettingName normalizes a setting name as ParseLongOption and the GUC
// lookup do together: '-' becomes '_', and case does not matter.
func pgSettingName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "-", "_"))
}

// truncateUTF8 cuts s to at most n bytes without splitting a multi-byte
// character, so a truncated value is still valid text in a JSON sink.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
