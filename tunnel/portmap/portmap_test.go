package portmap

import "testing"

func TestCanonicalPort(t *testing.T) {
	cases := []struct {
		subType string
		want    uint16
		ok      bool
	}{
		{"postgres", 5432, true},
		{"mysql", 3306, true},
		{"mssql", 1433, true},
		{"mongodb", 27017, true},
		{"oracledb", 1521, true},
		{"httpproxy", 80, true}, // plain HTTP into the tunnel; agent does TLS upstream
		{"tcp", 0, false},       // no canonical port for generic TCP
		{"ssh", 0, false},       // not tunnelable
		{"unknown", 0, false},   // unknown subtype
		{"", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.subType, func(t *testing.T) {
			got, ok := CanonicalPort(tc.subType)
			if got != tc.want || ok != tc.ok {
				t.Errorf("CanonicalPort(%q) = (%d, %v), want (%d, %v)", tc.subType, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestIsAcceptedPort(t *testing.T) {
	cases := []struct {
		name    string
		subType string
		port    uint16
		want    bool
	}{
		// canonical-port subtypes accept only the canonical port
		{"postgres on 5432", "postgres", 5432, true},
		{"postgres on 3306 (wrong)", "postgres", 3306, false},
		{"postgres on 22 (wrong)", "postgres", 22, false},
		{"mysql on 3306", "mysql", 3306, true},
		{"mysql on 5432 (wrong)", "mysql", 5432, false},
		{"mssql on 1433", "mssql", 1433, true},
		{"mongodb on 27017", "mongodb", 27017, true},
		{"oracledb on 1521", "oracledb", 1521, true},
		{"httpproxy on 80", "httpproxy", 80, true},
		// https://name.hoop can never work (no *.hoop certificate);
		// reject at the SYN so clients fail fast.
		{"httpproxy on 443 (wrong)", "httpproxy", 443, false},
		{"httpproxy on 8080 (wrong)", "httpproxy", 8080, false},

		// tcp accepts any non-zero port
		{"tcp on 22", "tcp", 22, true},
		{"tcp on 8080", "tcp", 8080, true},
		{"tcp on 65535", "tcp", 65535, true},
		{"tcp on 0 (invalid)", "tcp", 0, false},

		// not-tunnelable subtypes always reject
		{"ssh on 22", "ssh", 22, false},
		{"kubernetes on 6443", "kubernetes", 6443, false},
		{"rdp on 3389", "rdp", 3389, false},

		// zero port always rejected (defensive)
		{"postgres on 0", "postgres", 0, false},

		// unknown subtype rejected
		{"unknown subtype", "weird-subtype", 5432, false},
		{"empty subtype", "", 5432, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsAcceptedPort(tc.subType, tc.port)
			if got != tc.want {
				t.Errorf("IsAcceptedPort(%q, %d) = %v, want %v", tc.subType, tc.port, got, tc.want)
			}
		})
	}
}
