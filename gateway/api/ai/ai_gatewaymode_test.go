package apiai

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
)

// appconfig.Load runs once per test binary, so this package's tests run as a
// gateway. models.DB stays nil: a read of it panics.
func TestMain(m *testing.M) {
	if err := appconfig.Load(appconfig.AppModeGateway); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// A gateway holds no statement, so an analyzer save carries no limit to the
// approval rule and a response reads none back.
func TestAGatewayCarriesNoHoldTTLs(t *testing.T) {
	pending, approval := 900, 600
	groups := []string{"dba"}
	req := openapi.AISessionAnalyzerRuleRequest{PendingTTLSec: &pending, ApprovalTTLSec: &approval, ReviewersGroups: &groups}
	if p, a := holdTTLs(req); p != nil || a != nil {
		t.Fatalf("holdTTLs = %v/%v, want nil/nil", p, a)
	}
	if got := holdReviewers(req); got != nil {
		t.Fatalf("holdReviewers = %v, want nil", got)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("storedHold read the database: %v", r)
		}
	}()
	hold, err := storedHold(uuid.New(), "hold-writes")
	if hold != nil || err != nil {
		t.Fatalf("storedHold = %v, %v; want nil, nil", hold, err)
	}
	var out openapi.AISessionAnalyzerRule
	setHold(&out, hold)
	if out.ReviewersGroups != nil || out.PendingTTLSec != nil || out.ApprovalTTLSec != nil {
		t.Fatalf("setHold(nil) wrote %+v", out)
	}
}
