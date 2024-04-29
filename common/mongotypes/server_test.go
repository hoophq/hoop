package mongotypes

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestAuthResponseReplyDecode(t *testing.T) {
	want := &AuthResponseReply{
		HelloOk:  true,
		IsMaster: true,
		TopologyVersion: TopologyVersion{
			Counter:   0,
			ProcessID: primitive.ObjectID{0x66, 0x2f, 0x81, 0x17, 0x1a, 0x8e, 0x4c, 0xad, 0xac, 0x86, 0x5f, 0xe7},
		},
		MaxBsonObjectSize:            16777216,
		MaxMessageSizeBytes:          48000000,
		MaxWriteBatchSize:            100000,
		LocalTime:                    primitive.DateTime(1714389275739),
		LogicalSessionTimeoutMinutes: 30,
		ConnectionID:                 3,
		MinWireVersion:               0,
		MaxWireVersion:               21,
		ReadOnly:                     false,
		SaslSupportedMechs:           primitive.A{"SCRAM-SHA-256", "SCRAM-SHA-1"},
		SpeculativeAuthenticate: &SASLResponse{
			ConversationID: 1,
			Done:           false,
			Payload:        []byte{0x72, 0x3d, 0x62, 0x37, 0x70, 0x54, 0x49, 0x5a, 0x41, 0x4c, 0x35, 0x4c, 0x68, 0x61, 0x33, 0x33, 0x2f, 0x33, 0x49, 0x4e, 0x4c, 0x56, 0x59, 0x6c, 0x31, 0x64, 0x43, 0x36, 0x51, 0x35, 0x71, 0x6d, 0x45, 0x6f, 0x48, 0x4f, 0x35, 0x58, 0x79, 0x69, 0x42, 0x78, 0x43, 0x65, 0x30, 0x71, 0x68, 0x61, 0x4a, 0x6f, 0x6b, 0x44, 0x45, 0x61, 0x6b, 0x76, 0x45, 0x4c, 0x52, 0x53, 0x4b, 0x6e, 0x31, 0x38, 0x6e, 0x32, 0x2c, 0x73, 0x3d, 0x47, 0x54, 0x74, 0x58, 0x54, 0x2b, 0x38, 0x5a, 0x5a, 0x4d, 0x44, 0x75, 0x49, 0x53, 0x64, 0x79, 0x62, 0x4e, 0x58, 0x63, 0x73, 0x31, 0x66, 0x77, 0x62, 0x45, 0x35, 0x43, 0x58, 0x53, 0x6e, 0x55, 0x62, 0x36, 0x4d, 0x52, 0x31, 0x41, 0x3d, 0x3d, 0x2c, 0x69, 0x3d, 0x31, 0x35, 0x30, 0x30, 0x30},
		},
		OK: 1,
	}
	data, _ := hex.DecodeString("460200000500000006000000010000000800000000000000000000000000000001000000220200000868656c6c6f4f6b00010869736d6173746572000103746f706f6c6f677956657273696f6e002d0000000770726f63657373496400662f81171a8e4cadac865fe712636f756e74657200000000000000000000106d617842736f6e4f626a65637453697a650000000001106d61784d65737361676553697a65427974657300006cdc02106d61785772697465426174636853697a6500a0860100096c6f63616c54696d65005b5490298f010000106c6f676963616c53657373696f6e54696d656f75744d696e75746573001e00000010636f6e6e656374696f6e49640003000000106d696e5769726556657273696f6e0000000000106d61785769726556657273696f6e001500000008726561644f6e6c790000047361736c537570706f727465644d65636873002d0000000230000e000000534352414d2d5348412d323536000231000c000000534352414d2d5348412d3100000373706563756c617469766541757468656e74696361746500a300000010636f6e766572736174696f6e4964000100000008646f6e650000057061796c6f6164007500000000723d62377054495a414c354c686133332f33494e4c56596c316443365135716d456f484f3558796942784365307168614a6f6b4445616b76454c52534b6e31386e322c733d47547458542b385a5a4d447549536479624e5863733166776245354358536e5562364d5231413d3d2c693d313530303000016f6b00000000000000f03f00")
	replyPkt, _ := Decode(bytes.NewBuffer(data))
	got, err := DecodeServerAuthReply(replyPkt)
	if err != nil {
		t.Fatalf("expect to decode server auth reply without error, got=%v", err)
	}

	if !reflect.DeepEqual(want, got) {
		t.Errorf("auth response reply did not match, want=%#v, got=%#v", want, got)
	}

}
