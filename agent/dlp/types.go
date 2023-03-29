package dlp

import (
	"bytes"
	dlp "cloud.google.com/go/dlp/apiv2"
	"cloud.google.com/go/dlp/apiv2/dlppb"
	"context"
	pbdlp "github.com/runopsio/hoop/common/dlp"
	pb "github.com/runopsio/hoop/common/proto"
)

const (
	defaultRequestTimeoutInSec = 5
	defaultMaskingCharacter    = "*"
	defaultNumberToMask        = 5
	// this values needs to be low to avoid the max limit of findings per chunk
	// https://cloud.google.com/dlp/docs/deidentify-sensitive-data#findings-limit
	defaultMaxChunkSize = 62500
	maxFindings         = 2900
	maxInfoTypes        = 30
)

type (
	Client interface {
		DeidentifyContent(context.Context, *deidentifyConfig, int, *inputData) *Chunk
		ProjectID() string
	}
	inputData struct {
		inputTable  *dlppb.Table
		inputBuffer *bytes.Buffer
	}
	redactPostgresMiddleware struct {
		dlpClient       Client
		dataRowPackets  *bytes.Buffer
		typedPackets    *bytes.Buffer
		infoTypes       []*dlppb.InfoType
		maxRows         int
		maxPacketLength int
		rowCount        int
	}

	Chunk struct {
		index                 int
		transformationSummary *pbdlp.TransformationSummary
		data                  *bytes.Buffer
	}
	client struct {
		dlpClient *dlp.Client
		projectID string
	}
	streamWriter struct {
		client     pb.ClientTransport
		dlpClient  Client
		packetType pb.PacketType
		packetSpec map[string][]byte
		dlpConfig  *deidentifyConfig
		hookExec   pb.PluginHookExec
	}
	deidentifyConfig struct {
		// Character to use to mask the sensitive values, for example, `*` for an
		// alphabetic string such as a name, or `0` for a numeric string such as ZIP
		// code or credit card number. This string must have a length of 1. If not
		// supplied, this value defaults to `*`.
		maskingCharacter string
		// Number of characters to mask. If not set, all matching chars will be
		// masked. Skipped characters do not count towards this tally.
		//
		// If `number_to_mask` is negative, this denotes inverse masking. Cloud DLP
		// masks all but a number of characters.
		// For example, suppose you have the following values:
		//
		// - `masking_character` is `*`
		// - `number_to_mask` is `-4`
		// - `reverse_order` is `false`
		// - `CharsToIgnore` includes `-`
		// - Input string is `1234-5678-9012-3456`
		//
		// The resulting de-identified string is
		// `****-****-****-3456`. Cloud DLP masks all but the last four characters.
		// If `reverse_order` is `true`, all but the first four characters are masked
		// as `1234-****-****-****`.
		numberToMask int32
		projectID    string
		infoTypes    []*dlppb.InfoType
	}
)

func newTableInputData(data *dlppb.Table) *inputData {
	return &inputData{
		inputTable: data,
	}
}

func newBufferInputData(data *bytes.Buffer) *inputData {
	return &inputData{
		inputBuffer: data,
	}
}

func (i *inputData) contentItem() *dlppb.ContentItem {
	switch {
	case i.inputTable != nil:
		return &dlppb.ContentItem{DataItem: &dlppb.ContentItem_Table{Table: i.inputTable}}
	case i.inputBuffer != nil:
		defer i.inputBuffer.Reset()
		return &dlppb.ContentItem{
			DataItem: &dlppb.ContentItem_Value{
				Value: i.inputBuffer.String(),
			},
		}
	default:
		return nil
	}
}

func (c *Chunk) Data() *bytes.Buffer {
	return c.data
}

func (c *client) ProjectID() string {
	return c.projectID
}
