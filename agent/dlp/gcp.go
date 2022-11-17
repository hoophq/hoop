package dlp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"time"

	dlp "cloud.google.com/go/dlp/apiv2"
	"cloud.google.com/go/dlp/apiv2/dlppb"
	pb "github.com/runopsio/hoop/common/proto"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

func NewDLPClient(ctx context.Context, credentialsJSON []byte) (*Client, error) {
	creds, err := google.CredentialsFromJSON(
		ctx,
		credentialsJSON,
		"https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, err
	}
	client, err := dlp.NewClient(ctx, option.WithCredentials(creds))
	if err != nil {
		return nil, err
	}
	return &Client{client, creds.ProjectID}, nil
}

func NewDLPStreamWriter(client pb.ClientTransport,
	dlpClient *Client,
	packetType pb.PacketType,
	spec map[string][]byte,
	infoTypeList []string) *streamWriter {
	dlpConfig := &DeidentifyConfig{
		MaskingCharacter: defaultMaskingCharacter,
		NumberToMask:     defaultNumberToMask,
		InfoTypes:        parseInfoTypes(infoTypeList),
		ProjectID:        dlpClient.GetProjectID(),
	}
	return &streamWriter{
		client:     client,
		dlpClient:  dlpClient,
		packetType: packetType,
		packetSpec: spec,
		dlpConfig:  dlpConfig,
	}
}

func (s *streamWriter) Write(data []byte) (int, error) {
	p := &pb.Packet{Spec: map[string][]byte{}}
	if s.packetType == "" {
		return 0, fmt.Errorf("packet type must not be empty")
	}
	p.Type = s.packetType.String()
	p.Spec = s.packetSpec
	if s.dlpClient != nil && len(data) > 30 && len(s.dlpConfig.InfoTypes) > 0 {
		chunksBuffer := breakPayloadIntoChunks(bytes.NewBuffer(data))
		redactedChunks := redactChunks(s.dlpClient, s.dlpConfig, chunksBuffer)
		dataBuffer, tsList, err := joinChunks(redactedChunks)
		if err != nil {
			return 0, fmt.Errorf("failed joining chunks, err=%v", err)
		}
		if tsEnc, _ := pb.GobEncode(tsList); tsEnc != nil {
			p.Spec[pb.SpecDLPTransformationSummary] = tsEnc
		}
		p.Payload = dataBuffer.Bytes()
		return len(data), s.client.Send(p)
	}
	p.Payload = data
	return len(data), s.client.Send(p)
}

func (s *streamWriter) Close() error {
	_, _ = s.client.Close()
	return nil
}

// newDeindentifyContentRequest creates a new DeindentifyContentRequest with InspectConfig
// and DeidentifyConfig set
func newDeindentifyContentRequest(conf *DeidentifyConfig) *dlppb.DeidentifyContentRequest {
	return &dlppb.DeidentifyContentRequest{
		Parent: fmt.Sprintf("projects/%s/locations/global", conf.ProjectID),
		InspectConfig: &dlppb.InspectConfig{
			InfoTypes:     conf.InfoTypes,
			MinLikelihood: dlppb.Likelihood_POSSIBLE,
		},
		DeidentifyConfig: &dlppb.DeidentifyConfig{
			Transformation: &dlppb.DeidentifyConfig_InfoTypeTransformations{
				InfoTypeTransformations: &dlppb.InfoTypeTransformations{
					Transformations: []*dlppb.InfoTypeTransformations_InfoTypeTransformation{
						{
							InfoTypes: conf.InfoTypes, // Match all info types.
							PrimitiveTransformation: &dlppb.PrimitiveTransformation{
								Transformation: &dlppb.PrimitiveTransformation_CharacterMaskConfig{
									CharacterMaskConfig: &dlppb.CharacterMaskConfig{
										MaskingCharacter: conf.MaskingCharacter,
										NumberToMask:     conf.NumberToMask,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func deidentifyContent(ctx context.Context, client *Client, conf *DeidentifyConfig, chunkIndex int, data *bytes.Buffer) *Chunk {
	req := newDeindentifyContentRequest(conf)
	req.Item = &dlppb.ContentItem{
		DataItem: &dlppb.ContentItem_Value{
			Value: data.String(),
		},
	}
	r, err := client.DeidentifyContent(ctx, req)
	if err != nil {
		log.Printf("failed deidentify chunk (%v), err=%v", chunkIndex, err)
		return &Chunk{
			Index:                 chunkIndex,
			TransformationSummary: &TransformationSummary{Index: chunkIndex, Err: err},
			// return the chunk as non-redacted on errors
			data: data}
	}

	chunk := &Chunk{Index: chunkIndex, TransformationSummary: &TransformationSummary{Index: chunkIndex}}
	for _, s := range r.GetOverview().GetTransformationSummaries() {
		for _, r := range s.Results {
			result := []string{fmt.Sprintf("%v", r.Count), r.Code.String(), r.Details}
			chunk.TransformationSummary.SummaryResult = append(
				chunk.TransformationSummary.SummaryResult,
				result)
		}
		chunk.TransformationSummary.Summary = []string{
			s.InfoType.GetName(),
			fmt.Sprintf("%v", s.TransformedBytes)}
	}
	chunk.data = bytes.NewBufferString(r.Item.GetValue())
	return chunk
}

// redactChunks process chunks in parallel reordering after the end of each execution.
// A default timeout is applied for each chunk. If a requests timeout or returns an error the chunk is returned
// without redacting its content.
func redactChunks(client *Client, conf *DeidentifyConfig, chunksBuffer []*bytes.Buffer) []*Chunk {
	chunkCh := make(chan *Chunk)
	for idx, chunkBuf := range chunksBuffer {
		go func(idx int, chunkB *bytes.Buffer) {
			ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*defaultRequestTimeoutInSec)
			defer cancelFn()
			redactedChunk := deidentifyContent(ctx, client, conf, idx, chunkB)
			chunkCh <- redactedChunk
		}(idx, chunkBuf)
	}

	redactedChunks := make([]*Chunk, len(chunksBuffer))
	idx := 1
	for c := range chunkCh {
		redactedChunks[c.Index] = c
		if idx == len(chunksBuffer) {
			break
		}
		idx++
	}
	return redactedChunks
}

// joinChunks will recompose the chunks into a unique buffer along with a list of
// Transformations Summaries
func joinChunks(chunks []*Chunk) (*bytes.Buffer, []*TransformationSummary, error) {
	var tsList []*TransformationSummary
	res := bytes.NewBuffer([]byte{})
	for _, c := range chunks {
		if _, err := res.Write(c.data.Bytes()); err != nil {
			return nil, nil, fmt.Errorf("[dlp] failed writing chunk (%v) to buffer, err=%v", c.Index, err)
		}
		tsList = append(tsList, c.TransformationSummary)
	}
	return res, tsList, nil
}

func breakPayloadIntoChunks(payload *bytes.Buffer) []*bytes.Buffer {
	chunkSize := payload.Len()
	if chunkSize < maxChunkSize {
		return []*bytes.Buffer{payload}
	}
	var chunks []*bytes.Buffer
	if chunkSize > maxChunkSize {
		chunkSize = maxChunkSize
	}
	for {
		chunk := make([]byte, chunkSize)
		_, err := io.ReadFull(payload, chunk)
		if err != nil {
			break
		}
		chunks = append(chunks, bytes.NewBuffer(chunk))
	}
	return chunks
}

func parseInfoTypes(infoTypesList []string) []*dlppb.InfoType {
	var infoTypes []*dlppb.InfoType
	for _, infoType := range infoTypesList {
		if infoType == "" {
			continue
		}
		infoTypes = append(infoTypes, &dlppb.InfoType{Name: infoType})
	}
	return infoTypes
}
