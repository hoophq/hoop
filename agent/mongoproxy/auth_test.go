package mongoproxy

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.mongodb.org/mongo-driver/bson"
	// "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
)

type SaslRequest struct {
	IsMaster           int    `bson:"isMaster"`
	HelloOk            bool   `bson:"helloOk"`
	SaslSupportedMechs string `bson:"saslSupportedMechs"`
}

func TestBson(t *testing.T) {
	dataBytes, _ := hex.DecodeString(`050200007000000008000000010000000800000000000000000000000000000001000000e10100000868656c6c6f4f6b00010869736d6173746572000103746f706f6c6f677956657273696f6e002d0000000770726f6365737349640065eb35de78e488f28a17c76d12636f756e74657200000000000000000000106d617842736f6e4f626a65637453697a650000000001106d61784d65737361676553697a65427974657300006cdc02106d61785772697465426174636853697a6500a0860100096c6f63616c54696d6500e6caed1e8e010000106c6f676963616c53657373696f6e54696d656f75744d696e75746573001e00000010636f6e6e656374696f6e4964003f000000106d696e5769726556657273696f6e0000000000106d61785769726556657273696f6e001500000008726561644f6e6c7900000373706563756c617469766541757468656e74696361746500a300000010636f6e766572736174696f6e4964000100000008646f6e650000057061796c6f6164007500000000723d57633076634636517953493630536c31492f4c796c6f55796b3149426a6d76586657304d753358555a66676257314846746d4246596f677a5a366c544e4f62332c733d722f7157656e5745695463464d78354733357838734954426965634f4a454f6c6f32536c62513d3d2c693d313530303000016f6b00000000000000f03f00`)
	rawDoc, err := bson.ReadDocument(bytes.NewBuffer(dataBytes[36:]))
	if err != nil {
		t.Fatal(err)
	}
	var out AuthResponseReply
	if err := bson.Unmarshal(rawDoc, &out); err != nil {
		t.Fatal(err)
	}
	gotPkt, _ := bson.Marshal(&out)
	wantPkt := dataBytes[36:]
	if !cmp.Equal(gotPkt, wantPkt) {
		t.Error("payload does not match")
		fmt.Println("want -->>")
		fmt.Println(hex.Dump(wantPkt))
		fmt.Println("diff -->>")
		fmt.Println(cmp.Diff(hex.Dump(wantPkt), hex.Dump(gotPkt)))
		// fmt.Println(hex.Dump(dataOut))
		// fmt.Println("want -->>")
	}
	gotSize := out.Size()
	wantPktSize := len(wantPkt)
	if wantPktSize != gotSize {
		// fmt.Println(hex.Dump(wantPkt))
		t.Errorf("expect to match size, got=%v, want=%v, gotpktsize=%v", gotSize, wantPktSize, len(gotPkt))
	}

	// t.Errorf("RAWDOC: %#v", out)
}
