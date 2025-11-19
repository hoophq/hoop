package ssmproxy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
)

type AWS4SignatureHeader struct {
	Algorithm     string
	AccessKey     string
	Credential    string
	Date          string
	Region        string
	Service       string
	SignedHeaders []string
	Signature     string
}

func parseAWS4Header(authHeader string) (*AWS4SignatureHeader, error) {
	if !strings.HasPrefix(authHeader, "AWS4-HMAC-SHA256 ") {
		return nil, fmt.Errorf("invalid algorithm")
	}

	parts := strings.Split(authHeader[17:], ", ")
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid header format")
	}

	sig := &AWS4SignatureHeader{Algorithm: "AWS4-HMAC-SHA256"}

	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}

		key, value := kv[0], kv[1]

		switch key {
		case "Credential":
			credParts := strings.Split(value, "/")
			if len(credParts) != 5 {
				return nil, fmt.Errorf("invalid credential format: got %d parts, want 5", len(credParts))
			}
			sig.AccessKey = credParts[0]
			sig.Date = credParts[1]
			sig.Region = credParts[2]
			sig.Service = credParts[3]
			sig.Credential = value

		case "SignedHeaders":
			sig.SignedHeaders = strings.Split(value, ";")

		case "Signature":
			sig.Signature = value
		}
	}

	if sig.AccessKey == "" || sig.Signature == "" {
		return nil, fmt.Errorf("missing required fields")
	}

	return sig, nil
}

func createCanonicalRequest(
	method string,
	path string,
	query string,
	headers map[string]string,
	signedHeadersList []string,
	bodyHash string,
) string {
	// Normalize path
	if path == "" {
		path = "/"
	}

	// Sort signed headers
	sortedHeaders := make([]string, len(signedHeadersList))
	copy(sortedHeaders, signedHeadersList)
	sort.Strings(sortedHeaders)

	// Build canonical headers in sorted order
	canonicalHeaders := ""
	for _, hdr := range sortedHeaders {
		lowerHdr := strings.ToLower(hdr)
		value := headers[lowerHdr]
		canonicalHeaders += fmt.Sprintf("%s:%s\n", lowerHdr, value)
	}

	// Build signed headers string (also sorted)
	signedHeadersStr := strings.Join(sortedHeaders, ";")

	// Assemble canonical request
	canonicalRequest := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n%s\n%s",
		method,
		path,
		query,
		canonicalHeaders,
		signedHeadersStr,
		bodyHash,
	)

	return canonicalRequest
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Hash(data string) string {
	h := sha256.New()
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func validateAWS4Signature(
	c *gin.Context,
	secretKey string,
	authHeader *AWS4SignatureHeader,
) bool {
	// Read and buffer body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Errorf("❌ Error reading body: %v\n", err)
		return false
	}

	// Restore body for handler to use
	c.Request.Body = io.NopCloser(strings.NewReader(string(body)))

	// Get or calculate body hash
	bodyHash := c.GetHeader("X-Amz-Content-Sha256")
	if bodyHash == "" {
		bodyHash = sha256Hash(string(body))
	}

	headersMap := make(map[string]string)
	for _, hdrName := range authHeader.SignedHeaders {
		lowerName := strings.ToLower(hdrName)

		var val string

		// Special handling for Host header
		if lowerName == "host" {
			// The Host header comes from the request line, not headers
			// It should be: hostname[:port]
			if c.Request.Host != "" {
				val = c.Request.Host
			} else if h := c.Request.Header.Get("Host"); h != "" {
				val = h
			} else {
				// Fallback: construct from URL
				val = c.Request.URL.Host
				if val == "" && c.Request.URL.Hostname() != "" {
					val = c.Request.URL.Hostname()
					if c.Request.URL.Port() != "" {
						val = val + ":" + c.Request.URL.Port()
					}
				}
			}
		} else {
			// For other headers, use Values() to get all values
			vals := c.Request.Header.Values(hdrName)
			if len(vals) > 0 {
				val = strings.Join(vals, ",")
			}
		}

		if val != "" {
			headersMap[lowerName] = strings.TrimSpace(val)
		}
	}

	// Create canonical request
	canonicalRequest := createCanonicalRequest(
		c.Request.Method,
		c.Request.URL.Path,
		c.Request.URL.RawQuery,
		headersMap,
		authHeader.SignedHeaders,
		bodyHash,
	)

	// Get X-Amz-Date header
	amzDate := c.GetHeader("X-Amz-Date")
	if amzDate == "" {
		log.Errorf("Missing X-Amz-Date header\n")
		return false
	}

	// Extract datestamp
	datestamp := strings.Split(amzDate, "T")[0]

	// Build credential scope
	credentialScope := fmt.Sprintf(
		"%s/%s/%s/aws4_request",
		datestamp,
		authHeader.Region,
		authHeader.Service,
	)

	// Hash canonical request
	canonicalRequestHash := sha256Hash(canonicalRequest)

	// Build string to sign
	stringToSign := fmt.Sprintf(
		"AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzDate,
		credentialScope,
		canonicalRequestHash,
	)

	// Calculate signature
	kDate := hmacSHA256([]byte("AWS4"+secretKey), datestamp)
	kRegion := hmacSHA256(kDate, authHeader.Region)
	kService := hmacSHA256(kRegion, authHeader.Service)
	kSigning := hmacSHA256(kService, "aws4_request")
	calculatedSignature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	return calculatedSignature == authHeader.Signature
}
