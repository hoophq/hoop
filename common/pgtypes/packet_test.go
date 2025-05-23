package pgtypes

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseQuery(t *testing.T) {
	for _, tt := range []struct {
		msg      string
		hexQuery string
		expected []byte
	}{
		{
			msg:      "it must match extended query format",
			hexQuery: "50000000100053454c4543542031000000",
			expected: []byte("SELECT 1"),
		},
		{
			msg:      "it must match extended query format with complex query",
			hexQuery: "50000000ad002d2d204d657461626173653a3a207573657249443a2031207175657279547970653a204d42514c207175657279486173683a20356437313564636230343139343830626630363138653661353763623632353931303537646237393161653632353236333962373932333665343830653834640a53454c45435420434f554e54282a292041532022636f756e74222046524f4d20227075626c6963222e226f726465727322000000",
			expected: []byte(`-- Metabase:: userID: 1 queryType: MBQL queryHash: 5d715dcb0419480bf0618e6a57cb62591057db791ae6252639b79236e480e84d
SELECT COUNT(*) AS "count" FROM "public"."orders"`),
		},
		{
			msg:      "it must match extended query format with named query",
			hexQuery: "500000003f6765745f757365725f62795f69640053454c454354202a2046524f4d207573657273205748455245206964203d20243100000100000017",
			expected: []byte(`SELECT * FROM users WHERE id = $1`),
		},
		{
			msg:      "it must match simple query format",
			hexQuery: "510000003673656c656374202a2066726f6d20637573746f6d65727320776865726520656d61696c206c696b652027256a6f686e252700",
			expected: []byte(`select * from customers where email like '%john%'`),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			data, err := hex.DecodeString(tt.hexQuery)
			if err != nil {
				t.Fatal(err)
			}
			got := ParseQuery(data)
			assert.Equal(t, tt.expected, got)
		})
	}
}
