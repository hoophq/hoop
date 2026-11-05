package openapi

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The env vars of a review (private.reviews.input_env_vars) can hold runbook
// secrets. No review or session API type may expose them (ENG-1033).
//
// The test lists every JSON field the three types send. A new field fails it
// until a person checks that it carries no env vars and adds it here. A name
// check cannot prove that: a field named `inputs` would pass it.
var reviewSessionAPIFields = []string{
	"Review.access_duration",
	"Review.access_request_rule_name",
	"Review.approval_ttl_sec",
	"Review.connection.id",
	"Review.connection.name",
	"Review.created_at",
	"Review.expires_at",
	"Review.force_approval_groups",
	"Review.id",
	"Review.listener_name",
	"Review.min_approvals",
	"Review.rejection_reason",
	"Review.requester.email",
	"Review.requester.id",
	"Review.requester.name",
	"Review.requester.slack_id",
	"Review.review_groups_data.forced_review",
	"Review.review_groups_data.group",
	"Review.review_groups_data.id",
	"Review.review_groups_data.review_date",
	"Review.review_groups_data.reviewed_by.email",
	"Review.review_groups_data.reviewed_by.id",
	"Review.review_groups_data.reviewed_by.name",
	"Review.review_groups_data.reviewed_by.slack_id",
	"Review.review_groups_data.status",
	"Review.revoke_at",
	"Review.session",
	"Review.sidecar_id",
	"Review.status",
	"Review.time_window.configuration",
	"Review.time_window.type",
	"Review.type",
	"Session.ai_analysis.action",
	"Session.ai_analysis.explanation",
	"Session.ai_analysis.model",
	"Session.ai_analysis.risk_level",
	"Session.ai_analysis.steps.is_error",
	"Session.ai_analysis.steps.thinking",
	"Session.ai_analysis.steps.timestamp",
	"Session.ai_analysis.steps.tool_input",
	"Session.ai_analysis.steps.tool_name",
	"Session.ai_analysis.steps.tool_output",
	"Session.ai_analysis.steps.type",
	"Session.ai_analysis.summary",
	"Session.ai_analysis.title",
	"Session.connection",
	"Session.connection_subtype",
	"Session.connection_tags",
	"Session.correlation_id",
	"Session.end_date",
	"Session.event_size",
	"Session.event_stream",
	"Session.exit_code",
	"Session.guardrails_info.direction",
	"Session.guardrails_info.elapsed",
	"Session.guardrails_info.matched_words",
	"Session.guardrails_info.message",
	"Session.guardrails_info.rule.pattern_regex",
	"Session.guardrails_info.rule.type",
	"Session.guardrails_info.rule.words",
	"Session.guardrails_info.rule_name",
	"Session.id",
	"Session.identity_type",
	"Session.integrations_metadata",
	"Session.labels",
	"Session.machine_identity_id",
	"Session.metadata",
	"Session.metrics",
	"Session.org_id",
	"Session.recording_format",
	"Session.resource_name",
	"Session.review.access_duration",
	"Session.review.access_request_rule_name",
	"Session.review.created_at",
	"Session.review.force_approval_groups",
	"Session.review.id",
	"Session.review.listener_name",
	"Session.review.min_approvals",
	"Session.review.rejection_reason",
	"Session.review.review_groups_data.forced_review",
	"Session.review.review_groups_data.group",
	"Session.review.review_groups_data.id",
	"Session.review.review_groups_data.review_date",
	"Session.review.review_groups_data.reviewed_by.email",
	"Session.review.review_groups_data.reviewed_by.id",
	"Session.review.review_groups_data.reviewed_by.name",
	"Session.review.review_groups_data.reviewed_by.slack_id",
	"Session.review.review_groups_data.status",
	"Session.review.revoke_at",
	"Session.review.status",
	"Session.review.time_window.configuration",
	"Session.review.time_window.type",
	"Session.review.type",
	"Session.role_name",
	"Session.script",
	"Session.script_size",
	"Session.session_batch_id",
	"Session.start_date",
	"Session.status",
	"Session.type",
	"Session.user",
	"Session.user_id",
	"Session.user_name",
	"Session.verb",
	"SessionReview.access_duration",
	"SessionReview.access_request_rule_name",
	"SessionReview.created_at",
	"SessionReview.force_approval_groups",
	"SessionReview.id",
	"SessionReview.listener_name",
	"SessionReview.min_approvals",
	"SessionReview.rejection_reason",
	"SessionReview.review_groups_data.forced_review",
	"SessionReview.review_groups_data.group",
	"SessionReview.review_groups_data.id",
	"SessionReview.review_groups_data.review_date",
	"SessionReview.review_groups_data.reviewed_by.email",
	"SessionReview.review_groups_data.reviewed_by.id",
	"SessionReview.review_groups_data.reviewed_by.name",
	"SessionReview.review_groups_data.reviewed_by.slack_id",
	"SessionReview.review_groups_data.status",
	"SessionReview.revoke_at",
	"SessionReview.status",
	"SessionReview.time_window.configuration",
	"SessionReview.time_window.type",
	"SessionReview.type",
}

// Fields typed `any` cannot be checked here. A person must check each one.
// Today only client.Run reads models.Review.InputEnvVars; none of these
// maps is filled from it.
var reviewSessionAPIDynamicFields = map[string]bool{
	"Session.integrations_metadata": true,
	"Session.metadata":              true,
	"Session.metrics":               true,
}

func TestReviewAndSessionTypesDoNotExposeEnvVars(t *testing.T) {
	var got []string
	for _, typ := range []reflect.Type{
		reflect.TypeOf(Review{}),
		reflect.TypeOf(SessionReview{}),
		reflect.TypeOf(Session{}),
	} {
		got = append(got, collectJSONFields(t, typ, typ.Name(), map[reflect.Type]bool{})...)
	}
	sort.Strings(got)

	want := map[string]bool{}
	for _, f := range reviewSessionAPIFields {
		want[f] = true
	}
	for _, f := range got {
		if !want[f] {
			t.Errorf("new API field %s: check that it carries no review env vars, then add it to reviewSessionAPIFields", f)
		}
		delete(want, f)
	}
	for f := range want {
		t.Errorf("field %s is gone: remove it from reviewSessionAPIFields", f)
	}
}

// collectJSONFields returns the JSON path of each field reachable from typ.
// It descends only into structs of this package; a field typed `any` must be
// listed in reviewSessionAPIDynamicFields.
func collectJSONFields(t *testing.T, typ reflect.Type, path string, seen map[reflect.Type]bool) []string {
	t.Helper()
	typ = elemType(typ)
	if typ.Kind() != reflect.Struct || typ.PkgPath() != reflect.TypeOf(Review{}).PkgPath() || seen[typ] {
		return nil
	}
	seen[typ] = true
	defer delete(seen, typ)

	var fields []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "-" || !f.IsExported() {
			continue
		}
		if f.Anonymous && tag == "" {
			fields = append(fields, collectJSONFields(t, f.Type, path, seen)...)
			continue
		}
		if tag == "" {
			tag = f.Name
		}
		fpath := path + "." + tag
		if elemType(f.Type).Kind() == reflect.Interface && !reviewSessionAPIDynamicFields[fpath] {
			t.Errorf("%s is typed any and cannot be checked: give it a concrete type or list it in reviewSessionAPIDynamicFields", fpath)
		}
		sub := collectJSONFields(t, f.Type, fpath, seen)
		if len(sub) == 0 {
			fields = append(fields, fpath)
		}
		fields = append(fields, sub...)
	}
	return fields
}

func elemType(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice ||
		typ.Kind() == reflect.Map || typ.Kind() == reflect.Array {
		typ = typ.Elem()
	}
	return typ
}
