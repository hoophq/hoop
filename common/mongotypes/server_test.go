package mongotypes

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestDecodeServerAuthReply(t *testing.T) {
	want := &AuthResponseReply{
		ClusterTime: bson.Raw{0x58, 0x0, 0x0, 0x0, 0x11, 0x63, 0x6c, 0x75, 0x73, 0x74, 0x65, 0x72, 0x54, 0x69, 0x6d, 0x65, 0x0, 0x1d, 0x0, 0x0, 0x0, 0x72, 0x89, 0x33, 0x66, 0x3, 0x73, 0x69, 0x67, 0x6e, 0x61, 0x74, 0x75, 0x72, 0x65, 0x0, 0x33, 0x0, 0x0, 0x0, 0x5, 0x68, 0x61, 0x73, 0x68, 0x0, 0x14, 0x0, 0x0, 0x0, 0x0, 0x52, 0xed, 0xef, 0xaf, 0x73, 0xc0, 0x7e, 0x3a, 0xe3, 0x92, 0x24, 0x11, 0xd7, 0x47, 0x3a, 0x39, 0xb, 0x22, 0xf5, 0xd1, 0x12, 0x6b, 0x65, 0x79, 0x49, 0x64, 0x0, 0x2, 0x0, 0x0, 0x0, 0x6c, 0xd8, 0xae, 0x65, 0x0, 0x0}, // TODO
		HelloOK:     true,
		TopologyVersion: &TopologyVersion{
			Counter:   6,
			ProcessID: primitive.ObjectID{0x66, 0x33, 0x5c, 0x89, 0x26, 0x64, 0x83, 0x85, 0x00, 0x05, 0x7b, 0xa5},
		},
		Hosts: []string{
			"ac-ufkv9nm-shard-00-00.mwb5sun.mongodb.net:27017",
			"ac-ufkv9nm-shard-00-01.mwb5sun.mongodb.net:27017",
			"ac-ufkv9nm-shard-00-02.mwb5sun.mongodb.net:27017",
		},
		SetName:    "atlas-ocu9ec-shard-0",
		SetVersion: 18,
		IsMaster:   true,
		Secondary:  false,
		Primary:    "ac-ufkv9nm-shard-00-01.mwb5sun.mongodb.net:27017",
		Tags: map[string]string{
			"diskState":        "READY",
			"workloadType":     "OPERATIONAL",
			"nodeType":         "ELECTABLE",
			"provider":         "AWS",
			"availabilityZone": "use1-az6",
			"region":           "US_EAST_1",
		},
		Me:                           "ac-ufkv9nm-shard-00-01.mwb5sun.mongodb.net:27017",
		ElectionID:                   primitive.ObjectID{0x7f, 0xff, 0xff, 0xff, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x53},
		LastWrite:                    &lastWriteDate{time.Date(2024, time.May, 2, 12, 39, 14, 0, time.UTC)},
		MaxBSONObjectSize:            16777216,
		MaxMessageSizeBytes:          48000000,
		MaxWriteBatchSize:            100000,
		LogicalSessionTimeoutMinutes: 30,
		ConnectionID:                 10393,
		MinWireVersion:               0,
		MaxWireVersion:               21,
		ReadOnly:                     false,
		OK:                           1,
	}
	hexDec, _ := hex.DecodeString(`76040000574f7d007b040000010000000800000000000000000000000000000001000000520400000868656c6c6f4f6b000103746f706f6c6f677956657273696f6e002d0000000770726f6365737349640066335c892664838500057ba512636f756e7465720006000000000000000004686f73747300ad0000000230003100000061632d75666b76396e6d2d73686172642d30302d30302e6d77623573756e2e6d6f6e676f64622e6e65743a3237303137000231003100000061632d75666b76396e6d2d73686172642d30302d30312e6d77623573756e2e6d6f6e676f64622e6e65743a3237303137000232003100000061632d75666b76396e6d2d73686172642d30302d30322e6d77623573756e2e6d6f6e676f64622e6e65743a32373031370000027365744e616d65001500000061746c61732d6f63753965632d73686172642d30001073657456657273696f6e00120000000869736d61737465720001087365636f6e646172790000027072696d617279003100000061632d75666b76396e6d2d73686172642d30302d30312e6d77623573756e2e6d6f6e676f64622e6e65743a32373031370003746167730097000000026469736b5374617465000600000052454144590002776f726b6c6f616454797065000c0000004f5045524154494f4e414c00026e6f646554797065000a000000454c45435441424c45000270726f766964657200040000004157530002617661696c6162696c6974795a6f6e650009000000757365312d617a360002726567696f6e000a00000055535f454153545f310000026d65003100000061632d75666b76396e6d2d73686172642d30302d30312e6d77623573756e2e6d6f6e676f64622e6e65743a32373031370007656c656374696f6e4964007fffffff0000000000000153036c61737457726974650087000000036f7054696d65001c000000117473001d00000072893366127400530100000000000000096c6173745772697465446174650050e550398f010000036d616a6f726974794f7054696d65001c000000117473001d00000072893366127400530100000000000000096d616a6f726974795772697465446174650050e550398f01000000106d617842736f6e4f626a65637453697a650000000001106d61784d65737361676553697a65427974657300006cdc02106d61785772697465426174636853697a6500a0860100096c6f63616c54696d650033e750398f010000106c6f676963616c53657373696f6e54696d656f75744d696e75746573001e00000010636f6e6e656374696f6e49640099280000106d696e5769726556657273696f6e0000000000106d61785769726556657273696f6e001500000008726561644f6e6c790000016f6b00000000000000f03f0324636c757374657254696d65005800000011636c757374657254696d65001d00000072893366037369676e61747572650033000000056861736800140000000052edefaf73c07e3ae3922411d7473a390b22f5d1126b6579496400020000006cd8ae650000116f7065726174696f6e54696d65001d0000007289336600`)
	pkt, err := Decode(bytes.NewBuffer(hexDec))
	if err != nil {
		t.Fatalf("failed decoding packet, err=%v", err)
	}
	got, err := DecodeServerAuthReply(pkt)
	if err != nil {
		t.Fatalf("failed decoding server auth reply, err=%v", err)
	}
	if !cmp.Equal(want, got) {
		t.Errorf("expect object to match, diff\n%v", cmp.Diff(want, got))
	}

}

func TestNewScramServerDoneResponse(t *testing.T) {
	want, _ := hex.DecodeString(`790000000000000000000000dd07000000000000006400000010636f6e766572736174696f6e49640000000000057061796c6f6164002e00000000763d4a6d3273584279366853793054764b71356a526e467970754e327251387839786967412b4e623071674e343d08646f6e650001106f6b000100000000`)
	payload, _ := hex.DecodeString(`763d4a6d3273584279366853793054764b71356a526e467970754e327251387839786967412b4e623071674e343d`)
	got := NewScramServerDoneResponse(payload)
	if !bytes.Equal(got.Encode(), want) {
		t.Errorf("expected payload to match, got=%X, want=%X", got, want)
	}
}