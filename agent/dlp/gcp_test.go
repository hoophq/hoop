package dlp

import (
	"bytes"
	"context"
	"fmt"
	"testing"
)

type fakeClient struct {
	err error
}

func (c *fakeClient) DeidentifyContent(ctx context.Context, conf *deidentifyConfig, chunkIdx int, input *inputData) *Chunk {
	chunk := &Chunk{index: chunkIdx, transformationSummary: &transformationSummary{}}
	chunk.data = input.inputBuffer
	if c.err != nil {
		chunk.transformationSummary.err = c.err
	}
	return chunk
}

func (c *fakeClient) ProjectID() string { return "" }

func TestRedactChunks(t *testing.T) {
	for _, tt := range []struct {
		msg    string
		client *fakeClient
		chunks []*bytes.Buffer
	}{
		{
			msg:    "should return the same input buffer in the same order",
			client: &fakeClient{},
			chunks: []*bytes.Buffer{
				bytes.NewBuffer([]byte(`chunk-content-01`)),
				bytes.NewBuffer([]byte(`chunk-content-02`)),
				bytes.NewBuffer([]byte(`chunk-content-03`)),
			},
		},
		{
			msg:    "should return the same input buffer if found an error",
			client: &fakeClient{err: fmt.Errorf("failed redacting content")},
			chunks: []*bytes.Buffer{
				bytes.NewBuffer([]byte(`chunk-content-01`)),
				bytes.NewBuffer([]byte(`chunk-content-02`)),
			},
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			chunks := redactChunks(tt.client, &deidentifyConfig{}, tt.chunks)
			for i, c := range chunks {
				if !bytes.Equal(c.data.Bytes(), tt.chunks[i].Bytes()) {
					t.Errorf("chunks differs, got=%v, expected=%v", c.data.String(), tt.chunks[i].String())
				}
			}
			if tt.client.err != nil {
				for _, c := range chunks {
					if c.transformationSummary.err != tt.client.err {
						t.Errorf("should return error, got=%v, expected=%v", c.transformationSummary.err, tt.client.err)
					}
				}
			}
		})
	}
}

func TestBreakPayloadIntoChunks(t *testing.T) {
	for _, tt := range []struct {
		msg            string
		maxChunkSize   int
		payload        *bytes.Buffer
		expectedChunks int
	}{
		{
			msg:            "should return full content when payload < max size",
			maxChunkSize:   20,
			payload:        bytes.NewBuffer([]byte(`full-payload`)),
			expectedChunks: 1,
		},
		{
			msg:            "should break payload into chunks when payload > max size",
			maxChunkSize:   20,
			payload:        bytes.NewBuffer([]byte(`full-payload-that---should-be-broken`)),
			expectedChunks: 2,
		},
		{
			msg:            "the order should be preserved from left (begin) to right (end)",
			maxChunkSize:   6,
			payload:        bytes.NewBuffer([]byte(`a-more complex example break into several chunks`)),
			expectedChunks: 8,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			expectedPayload := bytes.NewBuffer(tt.payload.Bytes())
			chunksBuffer := breakPayloadIntoChunks(tt.payload, tt.maxChunkSize)
			if len(chunksBuffer) != tt.expectedChunks {
				t.Errorf("length of chunks differ, got=%v, expected=%v", len(chunksBuffer), tt.expectedChunks)
			}
			gotBuf := bytes.NewBuffer([]byte{})
			for _, c := range chunksBuffer {
				_, _ = gotBuf.Write(c.Bytes())
			}
			if gotBuf.String() != expectedPayload.String() {
				t.Errorf("joined buffer differs, got=%v, expected=%v", gotBuf.String(), expectedPayload.String())
			}
		})
	}
}
