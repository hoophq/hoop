package mssqltypes

import (
	"encoding/hex"
	"testing"
)

func TestSqlBatchDecode(t *testing.T) {
	for _, tt := range []struct {
		msg       string
		want      string
		pktStream string
	}{
		{
			msg:  "it should decode a sql statement (1)",
			want: "use adventureworks",
			// hex stream retrieved inspecting te flow of sql batch types on Wireshark
			pktStream: "010100420044010016000000120000000200000000000000000001000000750073006500200061006400760065006e00740075007200650077006f0072006b007300",
		},
		{
			msg:       "it should decode a sql statement (2)",
			want:      "select db_name() as a",
			pktStream: "010100480043010016000000120000000200000000000000000001000000730065006c006500630074002000640062005f006e0061006d0065002800290020006100730020006100",
		},
		{
			msg:       "it should decode a sql statement (3)",
			want:      "SELECT TOP 501 t.*\nFROM adventureworks.SalesLT.CustomerAddress t",
			pktStream: "0101009e0044010016000000120000000200000000000000000001000000530045004c00450043005400200054004f0050002000350030003100200074002e002a000a00460052004f004d00200061006400760065006e00740075007200650077006f0072006b0073002e00530061006c00650073004c0054002e0043007500730074006f006d0065007200410064006400720065007300730020007400",
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			data, _ := hex.DecodeString(tt.pktStream)
			got, err := DecodeSQLBatchToRawQuery(data)
			if err != nil {
				t.Fatalf("do not expect error when decoding sql batch packet, err=%v", err)
			}
			if tt.want != got {
				t.Errorf("expect to decode sql batch, want=%q, got=%q", tt.want, got)
			}
		})
	}
}
