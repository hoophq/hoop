package eventlogv1

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vmihailenco/msgpack/v5"
)

func date(hour, min int) time.Time {
	return time.Date(2024, time.June, 10, hour, min, 15, 23, time.UTC)
}

type msgPackStructTestInner struct {
	Name string `msgpack:"name"`
}

type msgPackStructTest struct {
	InfoType string                 `msgpack:"info_type"`
	Field    string                 `msgpack:"field"`
	Results  []string               `msgpack:"results"`
	Count    int64                  `msgpack:"count"`
	Inner    msgPackStructTestInner `msgpack:"inner"`
}

func encodeMsgPack(v msgPackStructTest) []byte {
	enc, _ := msgpack.Marshal(&v)
	return enc
}

func TestEncodeDecode(t *testing.T) {
	for _, tt := range []struct {
		msg     string
		want    *EventLog
		wantErr string
	}{
		{
			msg: "encode and decode it",
			want: New(date(10, 19), InputType, []byte(`ls -l`),
				map[string][]byte{
					"ab1": []byte(`val1`),
					"ab2": []byte(`val2`),
				},
			),
		},
		{
			msg:  "encode and decode it with nil data",
			want: New(date(10, 19), InputType, nil, nil),
		},
		{
			msg:  "encode and decode it with nil payload and metadata",
			want: New(date(10, 19), InputType, nil, map[string][]byte{"key": []byte(`val`)}),
		},
		{
			msg:  "encode and decode it with empty payload and nil metadata",
			want: New(date(10, 19), InputType, []byte(``), nil),
		},
		{
			msg:  "encode and decode it with payload and nil metadata",
			want: New(date(10, 19), InputType, nil, nil),
		},
		{
			msg:  "encode and decode it with empty metadata data",
			want: New(date(10, 19), InputType, []byte(`something`), map[string][]byte{"": []byte(``)}),
		},
		{
			msg:  "encode and decode it with nil metadata vals",
			want: New(date(10, 19), InputType, []byte(`something`), map[string][]byte{"key": nil}),
		},
		{
			msg:  "encode and decode it commit error",
			want: NewCommitError(date(10, 19), "failed commiting log to api"),
		},

		{
			msg: "encode and decode it msgpack",
			want: New(date(10, 19), InputType, []byte(`ls -l`),
				map[string][]byte{
					"datamasking-info": encodeMsgPack(msgPackStructTest{
						InfoType: "EMAIL", Field: "nothing", Results: []string{"one", "two"},
						Count: 9887217,
						Inner: msgPackStructTestInner{Name: "heyho"},
					}),
				},
			),
		},
		{
			msg:     "it should error with unknown event type",
			want:    New(date(10, 19), 'x', []byte(`ls -l`), nil),
			wantErr: ErrUnknownEventType.Error(),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			encEvent, err := tt.want.Encode()
			if err != nil {
				assert.EqualError(t, err, tt.wantErr, "it should match error")
				return
			}
			got, err := Decode(encEvent)
			if err != nil {
				assert.EqualError(t, err, tt.wantErr, "it should match error")
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDecodeWithBrokenData(t *testing.T) {
	testPayload := []byte{
		0x00, 0x00, 0x00, 0x30, 0x00, 0x00, 0x00, 0x05, 0x69, 0x17, 0xd7, 0x9d, 0x51, 0x36, 0xa7, 0x9e, // |...0....i...Q6..|
		0x17, 0x6c, 0x73, 0x20, 0x2d, 0x6c, 0x00, 0x00, 0x00, 0x00, 0x08, 0x61, 0x62, 0x31, 0x00, 0x76, // |.ls -l.....ab1.v|
		0x61, 0x6c, 0x00, 0x00, 0x00, 0x00, 0x09, 0x61, 0x62, 0x32, 0x00, 0x76, 0x61, 0x6c, 0x32, 0x00, // |al.....ab2.val2.|
	}
	newPayloadFn := func() []byte {
		v := make([]byte, len(testPayload))
		copy(v, testPayload)
		return v
	}
	for _, tt := range []struct {
		msg     string
		event   []byte
		want    *EventLog
		wantErr string
	}{
		{
			msg:     "it must be able to ignore any additional data",
			event:   append(newPayloadFn(), []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}...),
			wantErr: "",
		},
		{
			msg:     "it must error due to minimum length",
			event:   (newPayloadFn())[:8],
			wantErr: ErrDecodeMinimumLength.Error(),
		},
		{
			msg: "it must error due to payload length large then full log",
			// increase the length of the payload header from 0x05 to 0x30
			event:   func() []byte { v := newPayloadFn(); v[7] = 0x30; return v }(),
			wantErr: ErrPayloadLargerThanLog.Error(),
		},
		{
			msg: "it must error due to metadata header length not matching",
			// increase the length of the metadata header
			event:   func() []byte { v := newPayloadFn(); v[26]++; return v }(),
			wantErr: ErrMetadataLargerThanLog.Error(),
		},
		{
			msg: "it must error due to the log header length larger than the log itself",
			// increase the size of the log header
			event:   func() []byte { v := newPayloadFn(); v[3]++; return v }(),
			wantErr: ErrHeaderLengthLargerThanLog.Error(),
		},
		{
			msg: "it must error due to the header length small than the log",
			// decrease the size of the log header from 0x30 to 0x10
			event:   func() []byte { v := newPayloadFn(); v[3] = 0x10; return v }(),
			wantErr: "unable to decode header, reached max position=23, full-log-size=16",
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			_, err := Decode(tt.event)
			if tt.wantErr == "" && err == nil {
				err = fmt.Errorf("")
			}
			assert.EqualError(t, err, tt.wantErr, "it must match error")
		})
	}
}
