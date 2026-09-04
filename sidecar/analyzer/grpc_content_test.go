package analyzer

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

func TestGRPCBuilderRendersCapturedMessagesOnly(t *testing.T) {
	builder, ok := BuilderFor(inspect.GRPC)
	if !ok {
		t.Fatal("gRPC content builder is not registered")
	}

	header := inspect.Statement{
		Protocol: inspect.GRPC,
		HTTP:     &inspect.HTTPDetail{Resource: "/billing.v1.Invoices/Get"},
	}
	if _, ok := builder.Build(header, 1024); ok {
		t.Fatal("header-only statement produced model content")
	}

	message := header
	message.Direction = inspect.FromClient
	message.HTTP = &inspect.HTTPDetail{
		Resource:      "/billing.v1.Invoices/Get",
		Body:          `{"customer":{"tax_id":"123-45-6789"}}`,
		BodyTruncated: true,
	}
	content, ok := builder.Build(message, 1024)
	if !ok {
		t.Fatal("captured message produced no model content")
	}
	for _, want := range []string{
		"gRPC /billing.v1.Invoices/Get",
		"Direction: client",
		"(message truncated by the proxy)",
		`"tax_id":"123-45-6789"`,
	} {
		if !strings.Contains(content.Text, want) {
			t.Errorf("content %q does not contain %q", content.Text, want)
		}
	}
	if content.CacheKey == "" {
		t.Fatal("cache key is empty")
	}
}

func TestGRPCBuilderCacheSeparatesDirections(t *testing.T) {
	builder, _ := BuilderFor(inspect.GRPC)
	stmt := inspect.Statement{
		Protocol:  inspect.GRPC,
		Direction: inspect.FromClient,
		HTTP: &inspect.HTTPDetail{
			Resource: "/test.v1.Echo/Say",
			Body:     `{"value":"same"}`,
		},
	}
	request, _ := builder.Build(stmt, 1024)
	stmt.Direction = inspect.FromServer
	response, _ := builder.Build(stmt, 1024)
	if request.CacheKey == response.CacheKey {
		t.Fatal("request and response messages share a cache key")
	}
}
