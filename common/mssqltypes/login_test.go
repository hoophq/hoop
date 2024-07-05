package mssqltypes

import (
	"encoding/hex"
	"fmt"
	"testing"
)

func TestLoginEncodeDecode(t *testing.T) {
	for _, tt := range []struct {
		msg       string
		want      *login
		pktStream string
	}{
		{
			msg:       "it should decode a login7 packet",
			want:      &login{AppName: "sqlcmd", HostName: "Sandros-MacBook-Pro.local", Database: "adventureworks", header: &loginHeader{PacketSize: 4096, TDSVersion: 0x74000004}},
			pktStream: "100100fc00000100f40000000400007400100000000006010000000000000000a002000000000000000000005e0019009000030096000800a6000600b200090000000000c4000a00d8000000d8000e00000000000000f4000000f4000000f400000000000000530061006e00640072006f0073002d004d006100630042006f006f006b002d00500072006f002e006c006f00630061006c00730061006e00b6a5b3a586a583a596a593a5e6a5e3a5730071006c0063006d0064003100320037002e0030002e0030002e00310067006f002d006d007300730071006c006400620061006400760065006e00740075007200650077006f0072006b007300",
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			data, _ := hex.DecodeString(tt.pktStream)
			l := DecodeLogin([]byte(data[8:]))
			if l.AppName != tt.want.AppName || l.HostName != tt.want.HostName || l.Database != tt.want.Database ||
				l.header.PacketSize != tt.want.PacketSize() || l.header.TDSVersion != tt.want.TDSVersion() {
				t.Errorf("decoded values does not match [appname, hostname, database, packetsize, tdsversion], got=[%s %s %s %v %X]",
					l.AppName, l.HostName, l.Database, l.header.PacketSize, l.header.TDSVersion)
				fmt.Println(hex.Dump(data))
			}
			if _, err := EncodeLogin(*l); err != nil {
				t.Fatalf("do not expect error when re-encoding login7 packet, err=%v", err)
			}
		})
	}
}

func TestLoginDecodeDisablePasswordChange(t *testing.T) {
	data, _ := hex.DecodeString(`1001010c00000100040100000400007400100000000006010000000000000000a002000100000000000000005e0019009000030096000800a6000600b200090000000000c4000a00d8000000d8000e00000000000000f4000000f4000000f400080000000000530061006e00640072006f0073002d004d006100630042006f006f006b002d00500072006f002e006c006f00630061006c00730061006e00b6a5b3a586a583a596a593a5e6a5e3a5730071006c0063006d0064003100320037002e0030002e0030002e00310067006f002d006d007300730071006c006400620061006400760065006e00740075007200650077006f0072006b007300b6a5b3a586a583a596a593a5e6a5e3a5`)
	l := DecodeLogin(data[8:])
	if l.header.OptionFlags3&fChangePassword != 1 {
		t.Fatalf("change password flag must be set, got-optionflag3=%X", l.header.OptionFlags3)
	}
	l.DisablePasswordChange()
	pkt, _ := EncodeLogin(*l)
	l = DecodeLogin(pkt.Frame)
	if l.header.OptionFlags3&fChangePassword != 0 {
		t.Errorf("expect change password flag to be disabled, got-optionflag3=%X", l.header.OptionFlags3)
	}
}
