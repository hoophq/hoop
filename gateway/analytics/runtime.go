package analytics

import "os"

var (
	segmentApiKey   string
	intercomHmacKey string
)

func InitFromEnv() {
	segmentApiKey = os.Getenv("SEGMENT_WRITE_KEY")
	intercomHmacKey = os.Getenv("INTERCOM_HMAC_KEY")
}
