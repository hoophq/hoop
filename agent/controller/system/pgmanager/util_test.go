package pgmanager

import (
	"strings"
	"testing"
)

func TestQuoteLiteral(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"it's", "'it''s'"},
		{"", "''"},
		{"double''quote", "'double''''quote'"},
	}
	for _, tt := range tests {
		got := quoteLiteral(tt.input)
		if got != tt.want {
			t.Errorf("quoteLiteral(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestQuoteIdent(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", `"simple"`},
		{`say "hello"`, `"say ""hello"""`},
		{"", `""`},
	}
	for _, tt := range tests {
		got := quoteIdent(tt.input)
		if got != tt.want {
			t.Errorf("quoteIdent(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSplitPath(t *testing.T) {
	tests := []struct {
		input      string
		wantDB     string
		wantSchema string
		wantTable  string
	}{
		{"mydb", "mydb", "public", ""},
		{"mydb.myschema", "mydb", "myschema", ""},
		{"mydb.myschema.mytable", "mydb", "myschema", "mytable"},
		{"a.b.c.d", "", "", ""},
	}
	for _, tt := range tests {
		db, schema, table := splitPath(tt.input)
		if db != tt.wantDB || schema != tt.wantSchema || table != tt.wantTable {
			t.Errorf("splitPath(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tt.input, db, schema, table, tt.wantDB, tt.wantSchema, tt.wantTable)
		}
	}
}

func TestSplitDBSchema(t *testing.T) {
	tests := []struct {
		input      string
		wantDB     string
		wantSchema string
	}{
		{"mydb.myschema", "mydb", "myschema"},
		{"mydb", "mydb", ""},
		// IndexByte finds the first dot, so "a.b.c" splits at the first dot
		{"a.b.c", "a", "b.c"},
	}
	for _, tt := range tests {
		db, schema := splitDBSchema(tt.input)
		if db != tt.wantDB || schema != tt.wantSchema {
			t.Errorf("splitDBSchema(%q) = (%q, %q), want (%q, %q)",
				tt.input, db, schema, tt.wantDB, tt.wantSchema)
		}
	}
}

func TestSchemasOfInterest(t *testing.T) {
	scopes := []string{"mydb", "mydb.reporting", "otherdb.analytics"}
	result := schemasOfInterest(scopes)

	// "mydb" (bare) normalizes to "mydb.public"; "mydb.reporting" adds another
	mydbSchemas := result["mydb"]
	if len(mydbSchemas) != 2 {
		t.Fatalf("expected 2 schemas for mydb, got %d: %v", len(mydbSchemas), mydbSchemas)
	}
	// sorted: public, reporting
	if mydbSchemas[0] != "public" || mydbSchemas[1] != "reporting" {
		t.Errorf("unexpected mydb schemas: %v", mydbSchemas)
	}

	otherSchemas := result["otherdb"]
	if len(otherSchemas) != 1 || otherSchemas[0] != "analytics" {
		t.Errorf("unexpected otherdb schemas: %v", otherSchemas)
	}
}

func TestSchemasOfInterest_Deduplication(t *testing.T) {
	// Bare "shop" and "shop.public" both resolve to the public schema; deduped.
	scopes := []string{"shop", "shop.public"}
	result := schemasOfInterest(scopes)
	schemas := result["shop"]
	if len(schemas) != 1 || schemas[0] != "public" {
		t.Errorf("expected [public] after dedup, got %v", schemas)
	}
}

func TestSchemasOfInterest_EmptyInput(t *testing.T) {
	result := schemasOfInterest([]string{})
	if len(result) != 0 {
		t.Errorf("expected empty map for empty input, got %v", result)
	}
}

func TestStrSet(t *testing.T) {
	set := strSet([]string{"a", "b", "c", "b"})
	if len(set) != 3 {
		t.Errorf("expected 3 entries (b deduped), got %d", len(set))
	}
	for _, k := range []string{"a", "b", "c"} {
		if _, ok := set[k]; !ok {
			t.Errorf("expected %q in set", k)
		}
	}
}

func TestSortedKeys(t *testing.T) {
	m := map[string]int{"c": 3, "a": 1, "b": 2}
	got := sortedKeys(m)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sortedKeys[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSortedSet(t *testing.T) {
	m := map[string]struct{}{"c": {}, "a": {}, "b": {}}
	got := sortedSet(m)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sortedSet[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestChecksumSha256(t *testing.T) {
	// SHA256("hello") is a well-known value.
	sum, err := checksumSha256([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	const wantHello = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if sum != wantHello {
		t.Errorf("checksumSha256(\"hello\") = %s, want %s", sum, wantHello)
	}

	// Deterministic on repeated calls.
	sum2, _ := checksumSha256([]byte("hello"))
	if sum != sum2 {
		t.Error("checksum not deterministic")
	}

	// Different input produces different checksum.
	sumOther, _ := checksumSha256([]byte("world"))
	if sum == sumOther {
		t.Error("expected different checksum for different input")
	}
}

func TestRandomPassword(t *testing.T) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"

	pw, err := randomPassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(pw) != 25 {
		t.Errorf("expected length 25, got %d", len(pw))
	}
	for _, c := range pw {
		if !strings.ContainsRune(charset, c) {
			t.Errorf("character %q not in allowed charset", c)
		}
	}

	// Two successive calls should (astronomically) differ.
	pw2, err := randomPassword()
	if err != nil {
		t.Fatal(err)
	}
	if pw == pw2 {
		t.Error("successive randomPassword calls returned identical result")
	}
}

func TestDefaultAttributes(t *testing.T) {
	attrs := defaultAttributes()

	for _, k := range []string{"LOGIN", "INHERIT"} {
		if !attrs[k] {
			t.Errorf("expected %s=true in defaultAttributes", k)
		}
	}
	for _, k := range []string{"SUPERUSER", "BYPASSRLS", "CREATEDB", "CREATEROLE", "REPLICATION"} {
		if attrs[k] {
			t.Errorf("expected %s=false in defaultAttributes", k)
		}
	}
}

func TestConnURI(t *testing.T) {
	t.Run("user_password_and_port", func(t *testing.T) {
		c := ConnectionParts{
			Host:     "localhost",
			Port:     "5432",
			User:     "admin",
			Password: "s3cr3t",
		}
		uri := c.connURI("mydb")
		if !strings.HasPrefix(uri, "postgres://") {
			t.Errorf("expected postgres:// prefix, got %s", uri)
		}
		if !strings.Contains(uri, "localhost:5432") {
			t.Errorf("expected host:port in URI, got %s", uri)
		}
		if !strings.Contains(uri, "/mydb") {
			t.Errorf("expected /mydb path in URI, got %s", uri)
		}
	})

	t.Run("user_only_no_password", func(t *testing.T) {
		c := ConnectionParts{Host: "localhost", User: "admin"}
		uri := c.connURI("mydb")
		if !strings.Contains(uri, "admin@") {
			t.Errorf("expected user@ in URI, got %s", uri)
		}
		// Should not emit a colon-separated empty password.
		if strings.Contains(uri, ":@") {
			t.Errorf("unexpected colon-empty-password in URI: %s", uri)
		}
	})

	t.Run("no_port_omits_colon", func(t *testing.T) {
		c := ConnectionParts{Host: "pg.example.com"}
		uri := c.connURI("db")
		if strings.Contains(uri, "pg.example.com:") {
			t.Errorf("expected no port suffix when Port is empty, got %s", uri)
		}
	})

	t.Run("options_appear_in_query_string", func(t *testing.T) {
		c := ConnectionParts{
			Host:    "pg.example.com",
			User:    "admin",
			Options: map[string]string{"sslmode": "require"},
		}
		uri := c.connURI("testdb")
		if !strings.Contains(uri, "sslmode=require") {
			t.Errorf("expected sslmode in query string, got %s", uri)
		}
	})

	t.Run("options_sorted_for_stability", func(t *testing.T) {
		c := ConnectionParts{
			Host:    "localhost",
			Options: map[string]string{"zzz": "last", "aaa": "first"},
		}
		// Multiple calls must return the same string (map iteration order is random).
		uris := make([]string, 5)
		for i := range uris {
			uris[i] = c.connURI("db")
		}
		for i := 1; i < len(uris); i++ {
			if uris[i] != uris[0] {
				t.Errorf("connURI not stable across calls: %q vs %q", uris[0], uris[i])
			}
		}
		if !strings.Contains(uris[0], "aaa=first") || !strings.Contains(uris[0], "zzz=last") {
			t.Errorf("expected both options in URI: %s", uris[0])
		}
	})

	t.Run("target_db_appears_in_path", func(t *testing.T) {
		c := ConnectionParts{Host: "localhost"}
		if !strings.Contains(c.connURI("shop"), "/shop") {
			t.Error("expected /shop in URI path")
		}
		if !strings.Contains(c.connURI("warehouse"), "/warehouse") {
			t.Error("expected /warehouse in URI path")
		}
	})
}
