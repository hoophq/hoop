package hoopinspect_test

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/hoophq/hoopinspect"
	_ "github.com/hoophq/hoopinspect/codec/postgres"
	"github.com/hoophq/hoopinspect/policy"
)

// pgQuery builds a Postgres simple-query message, standing in for bytes read
// off a socket.
func pgQuery(sql string) []byte {
	var b bytes.Buffer
	b.WriteByte('Q')
	binary.Write(&b, binary.BigEndian, uint32(len(sql)+5))
	b.WriteString(sql)
	b.WriteByte(0)
	return b.Bytes()
}

// The end-to-end shape: bytes in, verdict out. Nothing here opens a socket or
// terminates TLS; whatever already holds the connection keeps holding it.
func Example() {
	rules, err := policy.NewRules([]policy.Rule{{
		Name:       "no-destructive",
		Type:       policy.MatchOperation,
		Operations: []hoopinspect.Operation{hoopinspect.OpDelete, hoopinspect.OpDrop},
		Message:    "destructive statements are not permitted on appdb",
	}})
	if err != nil {
		panic(err)
	}

	insp, err := hoopinspect.New(hoopinspect.Postgres)
	if err != nil {
		panic(err)
	}

	for _, sql := range []string{
		"SELECT name FROM customers",
		"DELETE FROM customers WHERE id = 1",
	} {
		stmts, err := insp.Inspect(hoopinspect.FromClient, pgQuery(sql))
		if err != nil {
			panic(err)
		}
		for _, s := range stmts {
			if v := rules.Evaluate(s); v.Denied {
				fmt.Printf("DENY  %-8s %v: %s\n", s.Operation, s.Tables, v.Message)
			} else {
				fmt.Printf("ALLOW %-8s %v\n", s.Operation, s.Tables)
			}
		}
	}

	// Output:
	// ALLOW select   [customers]
	// DENY  delete   [customers]: destructive statements are not permitted on appdb
}

// A single simple-query message may carry several statements. Each one is
// evaluated separately, so a policy denying DROP is not fooled by a harmless
// leading SELECT.
func Example_multiStatement() {
	insp, _ := hoopinspect.New(hoopinspect.Postgres)

	stmts, _ := insp.Inspect(hoopinspect.FromClient,
		pgQuery("SELECT 1; DROP TABLE users"))

	for _, s := range stmts {
		fmt.Printf("%s %v\n", s.Operation, s.Tables)
	}

	// Output:
	// select []
	// drop [users]
}

// Operation comes from a classifier that strips comments and string literals,
// so a keyword appearing inside data does not change the verdict. That is the
// concrete advantage over matching raw text.
func Example_literalsDoNotFoolClassification() {
	insp, _ := hoopinspect.New(hoopinspect.Postgres)

	stmts, _ := insp.Inspect(hoopinspect.FromClient,
		pgQuery("SELECT 'DROP TABLE customers' AS warning"))

	fmt.Println(stmts[0].Operation)

	// Output:
	// select
}
