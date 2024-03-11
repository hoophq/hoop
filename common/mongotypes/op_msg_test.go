package mongotypes

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestDecodeOpMsg(t *testing.T) {
	authRequestBytes, _ := hex.DecodeString(`940000000400000000000000dd07000000000100007f0000001068656c6c6f00010000000868656c6c6f4f6b000103746f706f6c6f677956657273696f6e002d0000000770726f6365737349640065e76f945efe25f537a1e6a312636f756e74657200000000000000000000126d6178417761697454696d654d5300102700000000000002246462000600000061646d696e0000`)
	pkt, err := Decode(bytes.NewBuffer(authRequestBytes))
	if err != nil {
		t.Fatalf("expected to decode packet, err=%v", err)
	}
	opMsg := DecodeOpMsg(pkt)
	if opMsg == nil {
		t.Fatal("expected non nil when decoding OP_MSG")
	}
	var out map[string]any
	err = opMsg.DecodeInto(&out)
	if err != nil {
		t.Fatalf("got error when decoding OP_MSG into map, err=%v", err)
	}
	if out["$db"] != "admin" {
		t.Errorf("expected to decode $db key in map, got=%+v", out)
	}
}
