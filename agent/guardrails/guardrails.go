package guardrails

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/hoophq/hoop/common/proto"
)

const (
	denyWordListType      string = "deny_words_list"
	patternMatchRegexType string = "pattern_match"
)

type ErrRuleMatch struct {
	name            string
	streamDirection string
}

func (e ErrRuleMatch) Error() string {
	if e.name != "" {
		return fmt.Sprintf("validation error, match guard rails %v rule, name=%v", e.streamDirection, e.name)
	}
	return fmt.Sprintf("validation error, match guard rails %v rule", e.streamDirection)
}

type DataRules struct {
	Items []Rule `json:"rules"`
}

type streamWriter struct {
	client          proto.ClientTransport
	packetType      proto.PacketType
	packetSpec      map[string][]byte
	dataRules       []DataRules
	streamDirection string
}

type Rule struct {
	Type         string   `json:"type"`
	Words        []string `json:"words"`
	PatternRegex string   `json:"pattern_regex"`
	Name         string   `json:"name"`
}

func (r *Rule) validate(streamDirection string, data []byte) error {
	switch r.Type {
	case denyWordListType:
		for _, word := range r.Words {
			if !strings.Contains(string(data), word) {
				continue
			}
			return &ErrRuleMatch{name: r.Name, streamDirection: streamDirection}
		}
	case patternMatchRegexType:
		regex, err := regexp.Compile(r.PatternRegex)
		if err != nil {
			return fmt.Errorf("failed parsing regex, reason=%v", err)
		}
		if regex.Match(data) {
			return &ErrRuleMatch{name: r.Name, streamDirection: streamDirection}
		}
	default:
		return fmt.Errorf("unknown rule type %q", r.Type)
	}
	return nil
}

func Decode(data []byte) ([]DataRules, error) {
	var root []DataRules
	if err := json.Unmarshal(data, &root); err != nil {
		return root, fmt.Errorf("unable to decode rules, reason=%v", err)
	}
	return root, nil
}

func Validate(streamDirection string, ruleData, data []byte) error {
	dataRules, err := Decode(ruleData)
	if err != nil {
		return err
	}
	for _, dataRule := range dataRules {
		for _, rule := range dataRule.Items {
			if err := rule.validate(streamDirection, data); err != nil {
				return err
			}
		}
	}
	return nil
}

func NewWriter(sid string, client proto.ClientTransport, pktType proto.PacketType, dataRules []DataRules, streamDirection string) io.WriteCloser {
	return &streamWriter{
		client:          client,
		packetSpec:      map[string][]byte{proto.SpecGatewaySessionID: []byte(sid)},
		packetType:      pktType,
		dataRules:       dataRules,
		streamDirection: streamDirection,
	}
}

func (w *streamWriter) Write(data []byte) (int, error) {
	if w.client == nil {
		return 0, fmt.Errorf("stream writer client is empty")
	}
	for _, dataRule := range w.dataRules {
		for _, rule := range dataRule.Items {
			if err := rule.validate(w.streamDirection, data); err != nil {
				return 0, err
			}
		}
	}
	return len(data), w.client.Send(&proto.Packet{
		Payload: data,
		Type:    w.packetType.String(),
		Spec:    w.packetSpec,
	})
}

func (s *streamWriter) AddSpecVal(key string, val []byte) {
	if s.packetSpec == nil {
		s.packetSpec = map[string][]byte{}
	}
	s.packetSpec[key] = val
}

func (s *streamWriter) Close() error {
	if s.client != nil {
		_, _ = s.client.Close()
	}
	return nil
}
