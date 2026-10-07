//go:build integration && parity

package parity

import (
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
)

// Run names one environment the scenario runs against. Every check declares
// which runs it covers and whether it must pass there today.
type Run string

const (
	// GatewayFlagOff is today's gateway: beta.sidecar_listeners off.
	GatewayFlagOff Run = "gateway-flag-off"
	// ControlPlane is today's control plane (`hoop start control-plane`).
	ControlPlane Run = "control-plane"
	// GatewayFlagOn is the target: the gateway with beta.sidecar_listeners on
	// serves what the control plane serves today.
	GatewayFlagOn Run = "gateway-flag-on"
)

// Expect is what a run expects of a check. A pending check names the ticket
// whose merge makes it pass; its failure is reported, not fatal.
type Expect struct {
	Pending string
}

// Must is a check that has to pass on the run today.
var Must = Expect{}

// PendingOn marks a check that waits for ticket to land on the run.
func PendingOn(ticket string) Expect { return Expect{Pending: ticket} }

// Check is one line of the "must not break" list.
type Check struct {
	ID    string
	Title string
	Runs  map[Run]Expect
	Fn    func(c *C)
}

var (
	registry   []Check
	checkIDRe  = regexp.MustCompile(`^[A-Z]{2,4}-\d{2}$`)
	ticketIDRe = regexp.MustCompile(`^[A-Z]+-\d+$`)
)

// register adds a check. Called from package-level var initializers, so a
// malformed check stops the test binary before any run starts.
func register(ck Check) bool {
	if !checkIDRe.MatchString(ck.ID) {
		panic(fmt.Sprintf("parity: check id %q must look like ABC-01", ck.ID))
	}
	if ck.Title == "" || ck.Fn == nil || len(ck.Runs) == 0 {
		panic(fmt.Sprintf("parity: check %s needs a title, a func and at least one run", ck.ID))
	}
	for run, exp := range ck.Runs {
		if exp.Pending != "" && !ticketIDRe.MatchString(exp.Pending) {
			panic(fmt.Sprintf("parity: check %s run %s: pending ticket %q is not a ticket id", ck.ID, run, exp.Pending))
		}
	}
	for _, existing := range registry {
		if existing.ID == ck.ID {
			panic(fmt.Sprintf("parity: duplicate check id %s", ck.ID))
		}
	}
	registry = append(registry, ck)
	return true
}

// C is the handle a check runs with. Fatalf stops the check; the runner turns
// the stop into a failure or, for a pending check, a skip.
type C struct {
	*Env
	id     string
	logs   []string
	failed string
}

type checkStopped struct{}

func (c *C) Fatalf(format string, args ...any) {
	c.failed = fmt.Sprintf(format, args...)
	panic(checkStopped{})
}

func (c *C) Logf(format string, args ...any) {
	c.logs = append(c.logs, fmt.Sprintf(format, args...))
}

// Name returns a resource name unique to this check, so checks that share an
// environment never collide.
func (c *C) Name(suffix string) string {
	return strings.ToLower(c.id) + "-" + suffix
}

// Outcome is the reported result of one check on one run.
type Outcome string

const (
	OutcomePass           Outcome = "pass"
	OutcomeFail           Outcome = "fail"
	OutcomePending        Outcome = "pending"
	OutcomeUnexpectedPass Outcome = "pass-pending"
)

type result struct {
	Run     Run
	ID      string
	Title   string
	Outcome Outcome
	Ticket  string
	Detail  string
}

// runCheck executes ck on env and records the result. A pending check runs in
// full: when it passes, the report says its marker can go.
func runCheck(t *testing.T, env *Env, ck Check, exp Expect, rep *report) {
	t.Helper()
	c := &C{Env: env, id: ck.ID}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(checkStopped); !ok {
					c.failed = fmt.Sprintf("panic: %v\n%s", r, debug.Stack())
				}
			}
		}()
		ck.Fn(c)
	}()
	for _, l := range c.logs {
		t.Log(l)
	}
	res := result{Run: env.Run, ID: ck.ID, Title: ck.Title, Ticket: exp.Pending, Detail: c.failed}
	switch {
	case c.failed == "" && exp.Pending == "":
		res.Outcome = OutcomePass
	case c.failed == "":
		res.Outcome = OutcomeUnexpectedPass
		t.Logf("passes while marked pending on %s: drop the marker", exp.Pending)
	case exp.Pending == "":
		res.Outcome = OutcomeFail
	default:
		res.Outcome = OutcomePending
	}
	rep.add(res)
	switch res.Outcome {
	case OutcomeFail:
		t.Fatalf("%s %s: %s", ck.ID, ck.Title, c.failed)
	case OutcomePending:
		t.Skipf("PENDING %s: %s", exp.Pending, c.failed)
	}
}
