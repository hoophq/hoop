package dlp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	dlp "cloud.google.com/go/dlp/apiv2"
	"cloud.google.com/go/dlp/apiv2/dlppb"
	"github.com/hoophq/pluginhooks"
	pbdlp "github.com/runopsio/hoop/common/dlp"
	pb "github.com/runopsio/hoop/common/proto"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

func NewDLPClient(ctx context.Context, credentialsJSON []byte) (*client, error) {
	creds, err := google.CredentialsFromJSON(
		ctx,
		credentialsJSON,
		"https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, err
	}
	dlpClient, err := dlp.NewClient(ctx, option.WithCredentials(creds))
	if err != nil {
		return nil, err
	}
	return &client{dlpClient, creds.ProjectID}, nil
}

func NewDLPStreamWriter(
	client pb.ClientTransport,
	hookExec pb.PluginHookExec,
	dlpClient Client,
	packetType pb.PacketType,
	spec map[string][]byte,
	infoTypeList []string) *streamWriter {
	dlpConfig := &deidentifyConfig{
		maskingCharacter: defaultMaskingCharacter,
		numberToMask:     defaultNumberToMask,
		infoTypes:        parseInfoTypes(infoTypeList),
		projectID:        dlpClient.ProjectID(),
	}
	return &streamWriter{
		client:     client,
		dlpClient:  dlpClient,
		packetType: packetType,
		packetSpec: spec,
		dlpConfig:  dlpConfig,
		hookExec:   hookExec,
	}
}

func (s *streamWriter) Write(data []byte) (int, error) {
	p := &pb.Packet{Spec: map[string][]byte{}}
	if s.packetType == "" {
		return 0, fmt.Errorf("packet type must not be empty")
	}
	// it should return the same amount of bytes of data
	// this will avoid having errors when using io.Copy function
	writeBytesLen := len(data)
	p.Type = s.packetType.String()
	p.Spec = s.packetSpec
	rpcOnSendFn := func() error {
		mutateData, err := s.hookExec.ExecRPCOnSend(&pluginhooks.Request{
			SessionID:  string(p.Spec[pb.SpecGatewaySessionID]),
			PacketType: p.Type,
			Payload:    p.Payload,
		})
		if len(mutateData) > 0 {
			p.Payload = mutateData
		}
		return err
	}
	if s.dlpClient != nil && len(data) > 30 && len(s.dlpConfig.infoTypes) > 0 {
		chunksBuffer := breakPayloadIntoChunks(bytes.NewBuffer(data), defaultMaxChunkSize)
		redactedChunks := redactChunks(s.dlpClient, s.dlpConfig, chunksBuffer)
		dataBuffer, tsList, err := joinChunks(redactedChunks)
		if err != nil {
			return 0, fmt.Errorf("failed joining chunks, err=%v", err)
		}
		if tsEnc, _ := pb.GobEncode(tsList); tsEnc != nil {
			p.Spec[pb.SpecDLPTransformationSummary] = tsEnc
		}
		p.Payload = dataBuffer.Bytes()
		if err := rpcOnSendFn(); err != nil {
			return 0, err
		}
		return writeBytesLen, s.client.Send(p)
	}
	p.Payload = data
	if err := rpcOnSendFn(); err != nil {
		return 0, err
	}
	return writeBytesLen, s.client.Send(p)
}

func (s *streamWriter) Close() error {
	_, _ = s.client.Close()
	return nil
}

func (c *client) DeidentifyContent(ctx context.Context, conf *deidentifyConfig, chunkIndex int, data *inputData) *Chunk {
	req := newDeindentifyContentRequest(conf)
	req.Item = data.contentItem()
	r, err := c.dlpClient.DeidentifyContent(ctx, req)
	if err != nil {
		return &Chunk{
			index:                 chunkIndex,
			transformationSummary: &pbdlp.TransformationSummary{Index: chunkIndex, Err: err}}
	}

	chunk := &Chunk{index: chunkIndex, transformationSummary: &pbdlp.TransformationSummary{Index: chunkIndex}}
	for _, s := range r.GetOverview().GetTransformationSummaries() {
		for _, r := range s.Results {
			result := []string{fmt.Sprintf("%v", r.Count), r.Code.String(), r.Details}
			chunk.transformationSummary.SummaryResult = append(
				chunk.transformationSummary.SummaryResult,
				result)
		}
		chunk.transformationSummary.Summary = []string{
			s.InfoType.GetName(),
			fmt.Sprintf("%v", s.TransformedBytes)}
	}

	responseTable := r.GetItem().GetTable()
	if responseTable != nil {
		dataRowsBuffer := encodeToDataRow(responseTable)
		return &Chunk{
			index:                 chunkIndex,
			transformationSummary: chunk.transformationSummary,
			data:                  dataRowsBuffer}
	}
	chunk.data = bytes.NewBufferString(r.Item.GetValue())
	return chunk
}

// redactChunks process chunks in parallel reordering after the end of each execution.
// A default timeout is applied for each chunk. If a requests timeout or returns an error the chunk is returned
// without redacting its content.
func redactChunks(client Client, conf *deidentifyConfig, chunksBuffer []*bytes.Buffer) []*Chunk {
	chunkCh := make(chan *Chunk)
	for idx, chunkBuf := range chunksBuffer {
		go func(idx int, chunkB *bytes.Buffer) {
			ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*defaultRequestTimeoutInSec)
			defer cancelFn()
			redactedChunk := client.DeidentifyContent(
				ctx, conf, idx,
				newBufferInputData(bytes.NewBuffer(chunkB.Bytes())))
			if redactedChunk.transformationSummary.Err != nil {
				redactedChunk.data = chunkB
			}
			chunkCh <- redactedChunk
		}(idx, chunkBuf)
	}

	redactedChunks := make([]*Chunk, len(chunksBuffer))
	idx := 1
	for c := range chunkCh {
		redactedChunks[c.index] = c
		if idx == len(chunksBuffer) {
			close(chunkCh)
			break
		}
		idx++
	}
	return redactedChunks
}

// joinChunks will recompose the chunks into a unique buffer along with a list of
// Transformations Summaries
func joinChunks(chunks []*Chunk) (*bytes.Buffer, []*pbdlp.TransformationSummary, error) {
	var tsList []*pbdlp.TransformationSummary
	res := bytes.NewBuffer([]byte{})
	for _, c := range chunks {
		if _, err := res.Write(c.data.Bytes()); err != nil {
			return nil, nil, fmt.Errorf("[dlp] failed writing chunk (%v) to buffer, err=%v", c.index, err)
		}
		tsList = append(tsList, c.transformationSummary)
	}
	return res, tsList, nil
}

func breakPayloadIntoChunks(payload *bytes.Buffer, maxChunkSize int) []*bytes.Buffer {
	chunkSize := payload.Len()
	if chunkSize < maxChunkSize {
		return []*bytes.Buffer{payload}
	}
	var chunks []*bytes.Buffer
	if chunkSize > maxChunkSize {
		chunkSize = maxChunkSize
	}
	read := payload.Len()
	for {
		if chunkSize > read {
			chunkSize = read
		}
		if chunkSize == 0 {
			break
		}
		chunk := make([]byte, chunkSize)
		n, err := io.ReadFull(payload, chunk)
		if err != nil {
			break
		}
		read -= n
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

func newDeindentifyContentRequest(conf *deidentifyConfig) *dlppb.DeidentifyContentRequest {
	return &dlppb.DeidentifyContentRequest{
		Parent: fmt.Sprintf("projects/%s/locations/global", conf.projectID),
		InspectConfig: &dlppb.InspectConfig{
			InfoTypes:     conf.infoTypes,
			MinLikelihood: dlppb.Likelihood_POSSIBLE,
		},
		DeidentifyConfig: &dlppb.DeidentifyConfig{
			Transformation: &dlppb.DeidentifyConfig_InfoTypeTransformations{
				InfoTypeTransformations: &dlppb.InfoTypeTransformations{
					Transformations: []*dlppb.InfoTypeTransformations_InfoTypeTransformation{
						{
							InfoTypes: conf.infoTypes, // Match all info types.
							PrimitiveTransformation: &dlppb.PrimitiveTransformation{
								Transformation: &dlppb.PrimitiveTransformation_CharacterMaskConfig{
									CharacterMaskConfig: &dlppb.CharacterMaskConfig{
										MaskingCharacter: conf.maskingCharacter,
										NumberToMask:     conf.numberToMask,
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
