package ssmproxy

// TODO: Move to separated package
import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
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
				return nil, fmt.Errorf("invalid credential format")
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
func createCanonicalRequest(c *gin.Context, authHeader *AWS4SignatureHeader, bodyHash string) string {
	method := c.Request.Method
	canonicalURI := c.Request.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQueryString := c.Request.URL.RawQuery
	// Build canonical headers (must be lowercase and sorted)
	headers := make(map[string]string)
	for _, hdr := range authHeader.SignedHeaders {
		val := c.GetHeader(hdr)
		if val != "" {
			headers[strings.ToLower(hdr)] = strings.TrimSpace(val)
		}
	}
	canonicalHeaders := ""
	for _, hdr := range authHeader.SignedHeaders {
		lowerHdr := strings.ToLower(hdr)
		canonicalHeaders += lowerHdr + ":" + headers[lowerHdr] + "\n"
	}
	signedHeadersStr := strings.Join(authHeader.SignedHeaders, ";")
	return fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		method, canonicalURI, canonicalQueryString, canonicalHeaders, signedHeadersStr, bodyHash)
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
func validateAWS4Signature(c *gin.Context, secretKey string, authHeader *AWS4SignatureHeader) bool {
	// Read and hash body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return false
	}
	// Restore body for handler to use
	c.Request.Body = io.NopCloser(strings.NewReader(string(body)))
	bodyHash := sha256Hash(string(body))
	// Create canonical request
	canonicalRequest := createCanonicalRequest(c, authHeader, bodyHash)
	// Get X-Amz-Date header
	amzDate := c.GetHeader("X-Amz-Date")
	if amzDate == "" {
		return false
	}
	datestamp := strings.Split(amzDate, "T")[0]
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request",
		datestamp, authHeader.Region, authHeader.Service)
	canonicalRequestHash := sha256Hash(canonicalRequest)
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzDate, credentialScope, canonicalRequestHash)
	// Calculate signature
	kDate := hmacSHA256([]byte("AWS4"+secretKey), datestamp)
	kRegion := hmacSHA256(kDate, authHeader.Region)
	kService := hmacSHA256(kRegion, authHeader.Service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))
	return signature == authHeader.Signature
}

// AWS4Auth Middleware for Gin
func AWS4Auth(secretKeyProvider func(accessKey string) (string, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			c.Abort()
			return
		}
		// Parse header
		sig, err := parseAWS4Header(authHeader)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid header: %v", err)})
			c.Abort()
			return
		}
		// Get secret key (you provide this function)
		secretKey, err := secretKeyProvider(sig.AccessKey)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid access key"})
			c.Abort()
			return
		}
		// Validate signature
		if !validateAWS4Signature(c, secretKey, sig) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "signature validation failed"})
			c.Abort()
			return
		}
		// Store parsed info in context for handler use
		c.Set("AccessKey", sig.AccessKey)
		c.Set("Region", sig.Region)
		c.Set("Service", sig.Service)
		c.Set("SignatureHeader", sig)
		c.Next()
	}
}
