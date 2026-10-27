package gate_test

import (
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/upstreamcred/upstreamcredtest"
)

// A gate whose codec cannot send the lane's credential would forward the
// client's copy instead, so New refuses it; an http codec takes it.
func TestNewHandsTheUpstreamCredentialToTheCodec(t *testing.T) {
	cred := upstreamcredtest.Open(t, "Authorization", "Bearer")
	if _, err := gate.New(newSession(), gate.Config{
		Protocol:           inspect.HTTP,
		CodecFactory:       func() inspect.Codec { return &lineCodec{} },
		UpstreamCredential: cred,
	}); err == nil {
		t.Error("New accepted a codec that cannot send the credential")
	}
	if _, err := gate.New(newSession(), gate.Config{
		Protocol:           inspect.HTTP,
		CodecFactory:       func() inspect.Codec { return codechttp.New(codechttp.Options{CredentialHeader: "authorization"}) },
		UpstreamCredential: cred,
	}); err == nil {
		t.Error("New accepted a credential in the header the codec lifts the caller from")
	}
	if _, err := gate.New(newSession(), gate.Config{
		Protocol:           inspect.HTTP,
		CodecFactory:       func() inspect.Codec { return codechttp.New(codechttp.Options{}) },
		UpstreamCredential: cred,
	}); err != nil {
		t.Errorf("New refused an http codec: %v", err)
	}
}
