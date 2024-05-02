package mongotypes

import (
	"bytes"
	"encoding/hex"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func TestPacketEncodingDecoding(t *testing.T) {
	encHex, _ := hex.DecodeString(`5a0100000100000000000000d40700000000000061646d696e2e24636d640000000000ffffffff330100001069736d617374657200010000000868656c6c6f4f6b000103636c69656e7400f0000000036170706c69636174696f6e001d000000026e616d65000e0000006d6f6e676f736820322e312e350000036472697665720037000000026e616d65000f0000006e6f64656a737c6d6f6e676f7368000276657273696f6e000c000000362e332e307c322e312e35000002706c6174666f726d00150000004e6f64652e6a73207632302e31312e312c204c4500036f73005b000000026e616d6500060000006c696e75780002617263686974656374757265000600000061726d3634000276657273696f6e0011000000352e31352e34392d6c696e75786b697400027479706500060000004c696e757800000004636f6d7072657373696f6e0011000000023000050000006e6f6e65000000`)
	pkt, err := Decode(bytes.NewBuffer(encHex))
	if err != nil {
		t.Fatal(err)
	}
	gotPktSize := int(pkt.MessageLength)
	wantPktSize := len(pkt.Frame) + 16
	if gotPktSize != wantPktSize {
		t.Errorf("frame packet does not match, want=%v, got=%v", wantPktSize, gotPktSize)
	}

	if !bytes.Equal(encHex, pkt.Encode()) {
		t.Errorf("packet frame must match, want=%X, got=%X", encHex[16:], pkt.Frame)
	}
}

func TestEncodeDecodeBsonToJsonNonCanonical(t *testing.T) {
	want := `{"hello":1,"helloOk":true,"topologyVersion":{"processId":{"$oid":"66314ea2a13a0bf9a6366d74"},"counter":6},"maxAwaitTimeMS":10000,"$db":"admin","$readPreference":{"mode":"primaryPreferred"}}`
	encHex, _ := hex.DecodeString(`c50000000400000000000000dd0700000000010000b00000001068656c6c6f00010000000868656c6c6f4f6b000103746f706f6c6f677956657273696f6e002d0000000770726f6365737349640066314ea2a13a0bf9a6366d7412636f756e74657200060000000000000000126d6178417761697454696d654d5300102700000000000002246462000600000061646d696e00032472656164507265666572656e63650020000000026d6f646500110000007072696d617279507265666572726564000000`)
	pkt, err := Decode(bytes.NewBuffer(encHex))
	if err != nil {
		t.Fatal(err)
	}
	var doc bson.D
	// skip message flags (4) and document kind body (1)
	if err := bson.Unmarshal(pkt.Frame[5:], &doc); err != nil {
		t.Fatal(err)
	}
	got, err := bson.MarshalExtJSON(doc, false, false)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != want {
		t.Errorf("expected marshal to match, got=%v, want=%v", string(got), want)
	}

}
