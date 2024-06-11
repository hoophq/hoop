package dlp

import (
	"bytes"
	"context"

	dlp "cloud.google.com/go/dlp/apiv2"
	"cloud.google.com/go/dlp/apiv2/dlppb"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/proto/spectypes"
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
		DeidentifyContent(ctx context.Context, config DeidentifyConfig, chunkIndex int, data InputData) *Chunk
		ProjectID() string
	}
	InputData interface {
		ContentItem() *dlppb.ContentItem
	}
	inputData struct {
		inputTable  *dlppb.Table
		inputBuffer *bytes.Buffer
	}

	Chunk struct {
		index                  int
		transformationOverview *spectypes.TransformationOverview
		data                   *bytes.Buffer
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
		dlpConfig  DeidentifyConfig
		hookExec   pb.PluginHookExec
	}
	DeidentifyConfig interface {
		// Character to use to mask the sensitive values, for example, `*` for an
		// alphabetic string such as a name, or `0` for a numeric string such as ZIP
		// code or credit card number. This string must have a length of 1. If not
		// supplied, this value defaults to `*`.
		MaskingCharacter() string
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
		NumberToMask() int
		ProjectID() string
		InfoTypes() []*dlppb.InfoType
	}
	deidentifyConfig struct {
		maskingCharacter string
		numberToMask     int
		projectID        string
		infoTypes        []*dlppb.InfoType
	}
)

func (c *deidentifyConfig) MaskingCharacter() string     { return c.maskingCharacter }
func (c *deidentifyConfig) NumberToMask() int            { return c.numberToMask }
func (c *deidentifyConfig) ProjectID() string            { return c.projectID }
func (c *deidentifyConfig) InfoTypes() []*dlppb.InfoType { return c.infoTypes }

func newBufferInputData(data *bytes.Buffer) *inputData {
	return &inputData{
		inputBuffer: data,
	}
}

func (i *inputData) ContentItem() *dlppb.ContentItem {
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

func (c *Chunk) Data() *bytes.Buffer { return c.data }
func (c *client) ProjectID() string  { return c.projectID }
func NewDeidentifyConfig(maskChar string, numberToMask int, projectID string, infoTypes []string) *deidentifyConfig {
	return &deidentifyConfig{
		maskingCharacter: maskChar,
		numberToMask:     numberToMask,
		projectID:        projectID,
		infoTypes:        parseInfoTypes(infoTypes),
	}
}
func NewChunk(data *bytes.Buffer) *Chunk { return &Chunk{data: data} }
func (c *Chunk) Error() error {
	if c.transformationOverview != nil {
		return c.transformationOverview.Err
	}
	return nil
}
func (c *Chunk) SetIndex(chunkIdx int) *Chunk {
	c.index = chunkIdx
	return c
}
func (c *Chunk) SetError(err error) *Chunk {
	if c.transformationOverview != nil {
		c.transformationOverview.Err = err
		return c
	}
	c.transformationOverview = &spectypes.TransformationOverview{Err: err}
	return c
}

func (c *Chunk) DataMaskingInfo() *spectypes.DataMaskingInfo {
	return &spectypes.DataMaskingInfo{Items: []*spectypes.TransformationOverview{c.transformationOverview}}
}
