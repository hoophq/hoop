package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoophq/hoop/hoopinspect/lexer"
	pgquery "github.com/wasilibs/go-pgquery"
)

// Robustness properties. No oracle, no expected values, nothing to keep in
// sync: these hold for every input or the scanner is broken.
//
//  1. Analyze never panics. It sits on a data path in front of a database.
//     A panic there is an outage, and the inputs are attacker-shaped.
//  2. A statement whose first word is a DML verb never comes back
//     Complete=true with no Effects. That combination reads as "I understood
//     the whole statement and it does nothing", which is a lie the caller
//     cannot detect. Not understanding is fine; it spells Complete=false.

var dialects = []lexer.Dialect{lexer.Postgres, lexer.MSSQL}

// hostile is input designed to walk the scanner off the end of something.
// Every entry is a construct that terminated a previous implementation early
// or left it inside a quote it never closed.
var hostile = []string{
	"",
	" ",
	"\x00",
	"\xff\xfe\xfd",
	"'",
	`"`,
	"`",
	"$$",
	"$tag$ unterminated",
	"$tag$ body $tag",
	"/*",
	"/* nested /* deeper",
	"--",
	"-- comment with no newline",
	"SELECT 'unterminated",
	`SELECT "unterminated`,
	"SELECT E'\\'",
	"SELECT N'x",
	"SELECT [unterminated",
	"SELECT tags[1] FROM t",
	"SELECT * FROM [dbo].[customers]",
	"(",
	")",
	"))))",
	"SELECT (((((",
	"WITH",
	"WITH x",
	"WITH x AS",
	"WITH x AS (",
	"DELETE",
	"DELETE FROM",
	"UPDATE",
	"UPDATE SET",
	"MERGE INTO",
	"COPY",
	"EXPLAIN",
	"DO",
	"CALL",
	"EXECUTE",
	";",
	";;;;",
	"SELECT 1;;SELECT 2",
	"\u00a0SELECT 1",           // non-breaking space where a space is expected
	"SEL\u200bECT 1",           // zero-width space inside a keyword
	"ＳＥＬＥＣＴ 1",                 // fullwidth letters
	"SELECT '\u0000' FROM t",   // NUL inside a literal
	"SELECT 1 /*+ hint */ + 2", // optimizer hint comment

	// Found by FuzzAnalyze. A DML keyword welded to a byte >= 0x80 is one
	// identifier, not a verb, and PostgreSQL rejects it. Kept here so the
	// invariant's word-splitting stays aligned with lexer.isWordByte.
	"UPDATE\x93",
	"DELETE\xc2\xa0FROM t",
	"SELECT\xff",
}

func TestAnalyzeNeverPanics(t *testing.T) {
	inputs := append([]string{}, hostile...)
	inputs = append(inputs, strings.Repeat("(", 10_000))
	inputs = append(inputs, strings.Repeat("SELECT 1;", 10_000))
	inputs = append(inputs, "SELECT "+strings.Repeat("a.", 10_000)+"b")
	inputs = append(inputs, strings.Repeat("WITH x AS (", 2_000))
	for _, c := range corpus() {
		inputs = append(inputs, c.sql)
	}

	for _, in := range inputs {
		for _, d := range dialects {
			// t.Run gives each input its own goroutine, so a panic names the
			// input that caused it instead of the whole test.
			t.Run(caseName(d.String()+" "+in), func(t *testing.T) {
				got := lexer.Analyze(in, d)
				if bad, why := violatesDMLInvariant(in, got); bad {
					t.Errorf("%s\n  input: %q", why, in)
				}
			})
		}
	}
}

func FuzzAnalyze(f *testing.F) {
	for _, in := range hostile {
		f.Add(in)
	}
	for _, c := range corpus() {
		f.Add(c.sql)
	}

	f.Fuzz(func(t *testing.T, sql string) {
		for _, d := range dialects {
			got := lexer.Analyze(sql, d)
			if bad, why := violatesDMLInvariant(sql, got); bad {
				t.Errorf("%s (dialect %s)", why, d)
			}
		}
	})
}

// violatesDMLInvariant reports the "understood it completely, it does
// nothing" contradiction described above.
func violatesDMLInvariant(sql string, a lexer.Analysis) (bool, string) {
	if !a.Complete || len(a.Effects) > 0 {
		return false, ""
	}
	switch firstWord(sql) {
	case "select", "insert", "update", "delete", "merge":
		return true, "Complete=true with no effects for a statement that starts with a DML verb"
	}
	return false, ""
}

// firstWord returns the leading identifier, lowercased.
//
// It ends the word on exactly the bytes a SQL lexer ends an identifier on:
// ASCII whitespace and ASCII punctuation. Any byte >= 0x80 CONTINUES the
// word, matching lexer.isWordByte.
//
// That rule is what keeps the invariant honest rather than merely strict.
// `UPDATE\x93` and `\u00a0SELECT 1` are single identifiers, so PostgreSQL
// rejects both as syntax errors and neither is a DML statement. A friendlier
// split would read "update" and "select" out of them and then blame the
// scanner for not finding a statement that was never there.
func firstWord(sql string) string {
	trimmed := strings.TrimLeft(sql, " \t\r\n\f\v")
	end := len(trimmed)
	for i := range len(trimmed) {
		if !isWordByte(trimmed[i]) {
			end = i
			break
		}
	}
	return strings.ToLower(trimmed[:end])
}

func isWordByte(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c >= 0x80
}

// TestPostgresRegressionCorpus measures the Complete rate over PostgreSQL's
// own regression suite.
//
// It asserts nothing about relations. The regress files exercise corners of
// the grammar no production client emits, and demanding the scanner match
// PostgreSQL there would be demanding it BE PostgreSQL. What the number is
// for is watching it move: a change that drops the Complete rate has taught
// the scanner to give up more often, and one that raises it without a
// corresponding oracle result has taught it to guess.
//
// The corpus is not vendored. 4.4 MB of another project's test fixtures does
// not belong in this tree, so the test skips unless you point it at a copy.
// README has the one-liner.
func TestPostgresRegressionCorpus(t *testing.T) {
	dir := os.Getenv("PG_REGRESS_SQL")
	if dir == "" {
		dir = filepath.Join("corpus", "regress")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	if len(files) == 0 {
		t.Skipf("no *.sql under %s; see README to populate it or set PG_REGRESS_SQL", dir)
	}

	var parsed, unusable, total, complete int
	reasons := map[string]int{}
	for _, path := range files {
		stmts, err := splitStatements(path)
		if err != nil {
			unusable++
			continue
		}
		parsed++
		for _, sql := range stmts {
			total++
			got := lexer.Analyze(sql, lexer.Postgres)
			if got.Complete {
				complete++
			} else {
				reasons[got.Reason]++
			}
			if bad, why := violatesDMLInvariant(sql, got); bad {
				t.Errorf("%s\n  file:  %s\n  input: %q", why, path, sql)
			}
		}
	}

	if total == 0 {
		t.Fatalf("found %d files under %s but no parseable statements", len(files), dir)
	}
	t.Logf("postgres regress: %d/%d files usable (%d rejected by pg itself), %d statements, %d complete (%.1f%%)",
		parsed, len(files), unusable, total, complete, 100*float64(complete)/float64(total))
	for reason, n := range reasons {
		t.Logf("  incomplete: %5d  %s", n, reason)
	}
}

// splitStatements cuts a regress file into individual statements using
// PostgreSQL's own statement boundaries.
//
// Splitting on ';' is wrong the moment a file contains a dollar-quoted body,
// and these files contain many. RawStmt carries the exact byte range, so the
// oracle that is already in this module does the splitting too. Files with
// psql meta-commands or deliberate syntax errors do not parse at all; they
// are reported as unusable rather than salvaged, because a half-recovered
// file is a corpus we invented.
func splitStatements(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	src := stripMetaCommands(string(data))
	tree, err := pgquery.Parse(src)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(tree.GetStmts()))
	for _, raw := range tree.GetStmts() {
		start := int(raw.GetStmtLocation())
		end := len(src)
		if n := int(raw.GetStmtLen()); n > 0 {
			end = start + n
		}
		if start < 0 || end > len(src) || start >= end {
			continue
		}
		if sql := strings.TrimSpace(src[start:end]); sql != "" {
			out = append(out, sql)
		}
	}
	return out, nil
}

// stripMetaCommands blanks psql backslash directives, which are not SQL and
// which appear in most regress files. Blanking rather than deleting keeps
// every byte offset RawStmt reports valid.
func stripMetaCommands(src string) string {
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), `\`) {
			lines[i] = strings.Repeat(" ", len(line))
		}
	}
	return strings.Join(lines, "\n")
}
