package dlp

import (
	"bytes"
	"fmt"

	dlp "cloud.google.com/go/dlp/apiv2"
	"cloud.google.com/go/dlp/apiv2/dlppb"
	pb "github.com/runopsio/hoop/common/proto"
)

const (
	defaultRequestTimeoutInSec = 5
	defaultMaskingCharacter    = "*"
	defaultNumberToMask        = 5
	// this values needs to be low to avoid the max limit of findings per chunk
	// https://cloud.google.com/dlp/limits#content-limits
	maxChunkSize = 62500
)

// var defaultInfoTypes = []*dlppb.InfoType{
// 	{Name: "PHONE_NUMBER"},
// 	{Name: "AGE"},
// 	{Name: "CREDIT_CARD_NUMBER"},
// 	{Name: "CREDIT_CARD_TRACK_NUMBER"},
// 	{Name: "DATE_OF_BIRTH"},
// 	{Name: "EMAIL_ADDRESS"},
// 	{Name: "ETHNIC_GROUP"},
// 	{Name: "GENDER"},
// 	{Name: "IBAN_CODE"},
// 	{Name: "HTTP_COOKIE"},
// 	{Name: "ICD9_CODE"},
// 	{Name: "ICD10_CODE"},
// 	{Name: "IMEI_HARDWARE_ID"},
// 	{Name: "IP_ADDRESS"},
// 	{Name: "STORAGE_SIGNED_URL"},
// 	{Name: "URL"},
// 	{Name: "VEHICLE_IDENTIFICATION_NUMBER"},
// 	{Name: "BRAZIL_CPF_NUMBER"},
// 	{Name: "AMERICAN_BANKERS_CUSIP_ID"},
// 	{Name: "FDA_CODE"},
// 	{Name: "US_ADOPTION_TAXPAYER_IDENTIFICATION_NUMBER"},
// 	{Name: "US_BANK_ROUTING_MICR"},
// 	{Name: "US_DEA_NUMBER"},
// 	{Name: "US_DRIVERS_LICENSE_NUMBER"},
// 	{Name: "US_EMPLOYER_IDENTIFICATION_NUMBER"},
// 	{Name: "US_HEALTHCARE_NPI"},
// 	{Name: "US_INDIVIDUAL_TAXPAYER_IDENTIFICATION_NUMBER"},
// 	{Name: "US_PASSPORT"},
// 	{Name: "US_PREPARER_TAXPAYER_IDENTIFICATION_NUMBER"},
// 	{Name: "US_SOCIAL_SECURITY_NUMBER"},
// 	{Name: "US_TOLLFREE_PHONE_NUMBER"},
// 	{Name: "US_VEHICLE_IDENTIFICATION_NUMBER"},
// }

type (
	TransformationSummary struct {
		Index int
		Err   error
		// [info-type, transformed-bytes]
		Summary []string
		// [[count, code, details] ...]
		SummaryResult [][]string
	}
	Chunk struct {
		Index                 int
		TransformationSummary *TransformationSummary
		data                  *bytes.Buffer
	}
	Client struct {
		*dlp.Client
		projectID string
	}
	streamWriter struct {
		client     pb.ClientTransport
		dlpClient  *Client
		packetType pb.PacketType
		packetSpec map[string][]byte
		infoTypes  []string
		dlpConfig  *DeidentifyConfig
	}
	DeidentifyConfig struct {
		// Character to use to mask the sensitive values, for example, `*` for an
		// alphabetic string such as a name, or `0` for a numeric string such as ZIP
		// code or credit card number. This string must have a length of 1. If not
		// supplied, this value defaults to `*`.
		MaskingCharacter string
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
		NumberToMask int32
		ProjectID    string
		InfoTypes    []*dlppb.InfoType
	}
)

func (c *Client) GetProjectID() string {
	return c.projectID
}

func (t *TransformationSummary) String() string {
	if len(t.Summary) == 2 {
		return fmt.Sprintf("chunk:%v, infotype:%v, transformedbytes:%v, result:%v",
			t.Index, t.Summary[0], t.Summary[1], t.SummaryResult)
	}
	if t.Err != nil {
		return fmt.Sprintf("chunk:%v, err:%v", t.Index, t.Err)
	}
	return ""
}
