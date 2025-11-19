package ssmproxy

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestAWSAuth(t *testing.T) {
	ctx, _ := gin.CreateTestContext(nil)
	u, _ := url.Parse("http://localhost:8009/ssm")
	ctx.Request = &http.Request{
		Header: make(http.Header),
		URL:    u,
	}
	ctx.Request.Header.Add("X-Amz-Target", "AmazonSSM.StartSession")
	ctx.Request.Header.Add("Content-Type", "application/x-amz-json-1.1")
	ctx.Request.Header.Add("Host", u.Host)
	ctx.Request.Header.Add("X-Amz-Date", "20251118T205629Z")
	ctx.Request.Header.Add("Authorization", "AWS4-HMAC-SHA256 Credential=AKIAADFXNQ6LBFBINL4KABNMVNVDAI/20251118/us-west-2/ssm/aws4_request, SignedHeaders=content-type;host;x-amz-date;x-amz-target, Signature=b2fa7f71befaf5c206b30df566830ff166f349157296f4dd8e93ffcb3df7aa0c")
	ctx.Request.Method = "POST"
	ctx.Request.URL.Path = "/ssm/"
	ctx.Request.Body = io.NopCloser(strings.NewReader(`{"Target": "i-0c32fa4f3d8835e0b"}`))
	secretKey := "a4084277390101f8f1a9987cb4e8a97883ebd123"

	authHeader, err := parseAWS4Header(ctx.Request.Header.Get("Authorization"))
	assert.NoError(t, err, "Failed to parse AWS4 header")

	isValid := validateAWS4Signature(ctx, secretKey, authHeader)
	assert.True(t, isValid, "Signature validation failed")
}
