package guardrails

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/hoophq/hoop/common/proto"
)

type streamWriter struct{}

type Rule struct {
	Type         string   `json:"type"`
	Words        []string `json:"words"`
	PatternRegex string   `json:"pattern_regex"`
}

type DataRules struct {
	Items []Rule `json:"rules"`
}

func Decode(data []byte) (DataRules, error) {
	var root DataRules
	if err := json.Unmarshal(data, &root); err != nil {
		return root, fmt.Errorf("unable to decode rules, reason=%v", err)
	}
	return root, nil
}

func Validate(ruleData, inputData []byte) error {
	root, err := Decode(ruleData)
	if err != nil {
		return err
	}
	fmt.Printf("decoded rules: %v\n", root)
	return fmt.Errorf("unable to validate rules, not implemented")
}

func NewWriter(sid string, client proto.ClientTransport, pktType proto.PacketType, inputRules []byte) io.WriteCloser {
	return nil
}

func (w *streamWriter) Write(data []byte) (int, error) {
	return 0, nil
}

func (w *streamWriter) Close() error {
	return nil
}
