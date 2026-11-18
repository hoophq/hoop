package analyzer

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/telemetry"
)

const (
	CheckTimeout       = 500 * time.Millisecond
	checkTTL           = 30 * time.Second
	checkCacheSize     = 4096
	maxCheckTables     = 8
	MaxCheckBlockBytes = 2048
)

const resourceCheckPrompt = `

The input can end with a "Resource check" block. The sidecar writes it from
the database catalog and the planner's estimates, before the statement runs;
the statement cannot change it. Use it to judge the effect on the resource:
reading or changing most rows of a large table, or scanning a large table with
no usable index, is riskier than the same statement on a small table or
through an index. The numbers are estimates. With no block, judge the
statement alone.`

type ResourceCheckEvent struct {
	Rule     string
	Duration time.Duration
	Tables   int
	Plan     bool

	// TablesErr and PlanErr are the parts the database refused.
	Err       error
	TablesErr string
	PlanErr   string
}

func checkable(op inspect.Operation) bool {
	switch op {
	case inspect.OpSelect, inspect.OpInsert, inspect.OpUpdate, inspect.OpDelete:
		return true
	}
	return false
}

func (e *Evaluator) UsesResourceCheck() bool { return e.cfg.ResourceCheck }

type planEntry struct {
	nodes []inspect.PlanNode
	err   string
}

func (e *Evaluator) resourceBlock(ctx context.Context, stmt inspect.Statement, ec *policy.EvalContext) string {
	if !e.cfg.ResourceCheck || ec == nil || ec.ResourceCheck == nil || !checkable(stmt.Operation) {
		return ""
	}
	rc := ec.ResourceCheck
	tables := checkTables(stmt.Tables)
	if len(tables) == 0 || rc.Skips(stmt) {
		return ""
	}
	scope := rc.Scope()
	facts := make(map[string]inspect.TableFacts, len(tables))
	var missing []string
	for _, t := range tables {
		if f, ok := e.facts.get(scope + "\x00" + t); ok {
			facts[t] = f
		} else {
			missing = append(missing, t)
		}
	}
	planKey := scope + "\x00" + stmt.Text
	plan, havePlan := e.plans.get(planKey)

	if (len(missing) > 0 || !havePlan) && e.budget.available(e.cfg.MaxCalls, e.cfg.RateLimit) {
		want := inspect.CheckWant{Tables: missing, Plan: !havePlan, Params: maxParam(stmt), Timeout: CheckTimeout}
		res, err := e.runCheck(ctx, rc, stmt, want, ec)
		if err == nil {
			if res.TablesErr == "" {
				for _, f := range res.Tables {
					e.facts.put(scope+"\x00"+f.Name, f)
					facts[f.Name] = f
				}
			}
			if want.Plan {
				plan, havePlan = planEntry{nodes: res.Plan, err: res.PlanErr}, true
				e.plans.put(planKey, plan)
			}
		}
	}

	var known []inspect.TableFacts
	for _, t := range tables {
		if f, ok := facts[t]; ok {
			known = append(known, f)
		}
	}
	var nodes []inspect.PlanNode
	if havePlan {
		nodes = plan.nodes
	}
	block := renderCheckBlock(known, nodes)
	if block == "" || e.cfg.Redact == nil {
		return block
	}
	if block = e.cfg.Redact(block); block == RefuseSentinel {
		e.reportCheck(ResourceCheckEvent{Rule: e.cfg.Rule, Err: errors.New("the redactor refused the check block; classified without it")})
		return ""
	}
	return block
}

func (e *Evaluator) runCheck(ctx context.Context, rc policy.ResourceChecker, stmt inspect.Statement, want inspect.CheckWant, ec *policy.EvalContext) (inspect.CheckResult, error) {
	checkCtx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	end := func(string, error) {}
	if ec.Telemetry != nil {
		_, end = ec.Telemetry.BeginStage(checkCtx, telemetry.StageResourceCheck, "")
	}
	start := time.Now()
	res, err := rc.Check(checkCtx, stmt, want)
	ev := ResourceCheckEvent{
		Rule: e.cfg.Rule, Duration: time.Since(start), Tables: len(want.Tables), Plan: want.Plan,
		Err: err, TablesErr: res.TablesErr, PlanErr: res.PlanErr,
	}
	switch {
	case errors.Is(err, policy.ErrCheckNotReady):
	case err != nil:
		end(telemetry.OutcomeError, err)
	default:
		end(telemetry.OutcomeOK, nil)
	}
	e.reportCheck(ev)
	return res, err
}

func (e *Evaluator) reportCheck(ev ResourceCheckEvent) {
	if e.cfg.OnResourceCheck != nil {
		e.cfg.OnResourceCheck(ev)
	}
}

func checkTables(in []string) []string {
	out := make([]string, 0, min(len(in), maxCheckTables))
	seen := make(map[string]bool, len(in))
	for _, t := range in {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) == maxCheckTables {
			break
		}
	}
	return out
}

func maxParam(stmt inspect.Statement) int {
	if stmt.Protocol != inspect.Postgres {
		return 0
	}
	n, _ := lexer.MaxParam(stmt.Text, lexer.Postgres)
	return n
}

// renderCheckBlock writes numbers to two significant digits, so the verdict
// cache key, which hashes the block, survives small statistics drift.
func renderCheckBlock(tables []inspect.TableFacts, plan []inspect.PlanNode) string {
	if len(tables) == 0 && len(plan) == 0 {
		return ""
	}
	lines := []string{"Resource check (database estimates, read before the statement runs):"}
	for _, t := range tables {
		lines = append(lines, tableLine(t))
	}
	if len(plan) > 0 {
		lines = append(lines, "Plan:")
		for _, n := range plan {
			lines = append(lines, strings.Repeat("  ", n.Depth+1)+planLine(n))
		}
	}
	var b strings.Builder
	for _, l := range lines {
		if b.Len()+len(l)+1 > MaxCheckBlockBytes {
			b.WriteString("(truncated)")
			break
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func tableLine(t inspect.TableFacts) string {
	if !t.Found {
		return "Table " + t.Name + ": not found"
	}
	rows := "rows unknown (never analyzed)"
	if t.Rows >= 0 {
		rows = "about " + approx(float64(t.Rows)) + " rows"
	}
	line := fmt.Sprintf("Table %s: %s, %s", t.Name, rows, approxBytes(t.Bytes))
	if len(t.Indexes) == 0 {
		return line + ", no indexes"
	}
	idx := make([]string, len(t.Indexes))
	for i, x := range t.Indexes {
		kind := ""
		switch {
		case x.Primary:
			kind = "primary key "
		case x.Unique:
			kind = "unique "
		}
		idx[i] = x.Name + " (" + kind + x.Definition + ")"
	}
	return line + ", indexes: " + strings.Join(idx, ", ")
}

func planLine(n inspect.PlanNode) string {
	s := n.Node
	if n.Relation != "" {
		s += " on " + n.Relation
	}
	if n.Index != "" {
		s += " using " + n.Index
	}
	return s + " (rows " + approx(n.Rows) + ", cost " + approx(n.Cost) + ")"
}

func approx(v float64) string {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return "0"
	}
	exp := math.Floor(math.Log10(v))
	v = math.Round(v/math.Pow(10, exp-1)) * math.Pow(10, exp-1)
	for _, u := range []struct {
		div    float64
		suffix string
	}{{1e9, "B"}, {1e6, "M"}, {1e3, "K"}} {
		if v >= u.div {
			return strconv.FormatFloat(v/u.div, 'f', -1, 64) + u.suffix
		}
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func approxBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return approx(float64(n)/(1<<30)) + " GB"
	case n >= 1<<20:
		return approx(float64(n)/(1<<20)) + " MB"
	case n >= 1<<10:
		return approx(float64(n)/(1<<10)) + " KB"
	}
	return approx(float64(n)) + " bytes"
}

type ttlCache[V any] struct {
	mu      sync.Mutex
	ttl     time.Duration
	size    int
	entries map[string]*list.Element
	order   *list.List
	now     func() time.Time
}

type ttlEntry[V any] struct {
	key     string
	val     V
	expires time.Time
}

func newTTLCache[V any](size int, ttl time.Duration) *ttlCache[V] {
	return &ttlCache[V]{ttl: ttl, size: size, entries: map[string]*list.Element{}, order: list.New(), now: time.Now}
}

func (c *ttlCache[V]) get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	el, ok := c.entries[key]
	if !ok {
		return zero, false
	}
	ent := el.Value.(*ttlEntry[V])
	if c.now().After(ent.expires) {
		c.order.Remove(el)
		delete(c.entries, key)
		return zero, false
	}
	return ent.val, true
}

func (c *ttlCache[V]) put(key string, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.order.Remove(el)
	}
	c.entries[key] = c.order.PushFront(&ttlEntry[V]{key: key, val: v, expires: c.now().Add(c.ttl)})
	for c.order.Len() > c.size {
		old := c.order.Back()
		c.order.Remove(old)
		delete(c.entries, old.Value.(*ttlEntry[V]).key)
	}
}
