package rdp

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEncodeDecodeIronStructs(t *testing.T) {
	// Test encoding and decoding of Iron structs
	testStruct := RDCleanPathPdu{
		Version:           uint64(rand.Int64()),
		Error:             &RDCleanPathError{},
		Destination:       ptrString("DEADBEEF 00000"),
		ProxyAuth:         ptrString("hueheueheuheu"),
		ServerAuth:        ptrString("12344321"),
		PreconnectionBlob: ptrString("blobblobblob"),
		X224ConnectionPDU: []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05},
		ServerCertChain: [][]byte{
			{0x00, 0x01, 0x02, 0x03, 0x04, 0x05},
			[]byte("deadbeef"),
		},
		ServerAddr: ptrString("192.168.0.1"),
	}
	der, err := marshalContextExplicit(testStruct)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	t.Logf("Encoded: %v", der)

	decoded := RDCleanPathPdu{}
	if err := unmarshalContextExplicit(der, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	assert.Equal(t, testStruct, decoded)
}
