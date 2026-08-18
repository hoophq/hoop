package sessionapi

import (
	"net/url"
	"testing"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What a request without ?count= gets is the API contract, so it is asserted
// against a literal rather than against NewSessionOption() — the table case for
// "an empty query string yields the defaults" compares to the constructor and
// would follow it silently wherever it moved.
func TestParseSessionListOptionsDefaultsToCappedCount(t *testing.T) {
	got, err := parseSessionListOptions(url.Values{})
	require.NoError(t, err)
	assert.Equal(t, models.SessionCountMode("capped"), got.CountMode,
		"GET /api/sessions without ?count= must not pay for an exact count")

	// And an explicit request for the expensive mode is still honoured.
	got, err = parseSessionListOptions(url.Values{"count": []string{"exact"}})
	require.NoError(t, err)
	assert.Equal(t, models.SessionCountExact, got.CountMode)
}

func TestParseSessionListOptions(t *testing.T) {
	for _, tt := range []struct {
		msg     string
		qs      string
		want    func(models.SessionOption) models.SessionOption
		wantErr string
	}{
		{
			msg:  "an empty query string yields the defaults",
			qs:   "",
			want: func(o models.SessionOption) models.SessionOption { return o },
		},
		{
			msg: "it accepts every filter",
			qs:  "user=alice&connection=pg-prod&type=database&review.status=PENDING&review.approver=a@b.c&batch_id=b1&correlation_id=c1",
			want: func(o models.SessionOption) models.SessionOption {
				approver, batch, correlation := "a@b.c", "b1", "c1"
				o.User, o.ConnectionName, o.ConnectionType, o.ReviewStatus = "alice", "pg-prod", "database", "PENDING"
				o.ReviewApproverEmail, o.BatchID, o.CorrelationID = &approver, &batch, &correlation
				return o
			},
		},
		{
			msg: "jira issue keys are split, trimmed and lowercased",
			qs:  "jira_issue_key=%20ABC-1%20,def-2",
			want: func(o models.SessionOption) models.SessionOption {
				o.JiraIssueKey = []string{"abc-1", "def-2"}
				return o
			},
		},
		{
			msg: "it accepts pagination within bounds",
			qs:  "limit=50&offset=10000",
			want: func(o models.SessionOption) models.SessionOption {
				o.Limit, o.Offset = 50, models.MaxSessionListOffset
				return o
			},
		},
		{
			msg: "a limit above the maximum is clamped, not rejected",
			qs:  "limit=5000",
			want: func(o models.SessionOption) models.SessionOption {
				o.Limit = models.MaxSessionListLimit
				return o
			},
		},
		{
			msg: "it accepts every count mode",
			qs:  "count=capped",
			want: func(o models.SessionOption) models.SessionOption {
				o.CountMode = models.SessionCountCapped
				return o
			},
		},
		{
			msg: "an end_date defaults to now when only start_date is given",
			qs:  "start_date=2026-01-01T00:00:00Z",
			want: func(o models.SessionOption) models.SessionOption {
				o.StartDate.String, o.StartDate.Valid = "2026-01-01T00:00:00Z", true
				// EndDate is time.Now(); asserted separately below.
				return o
			},
		},
		{
			msg: "an unset review status keeps the wildcard sentinel",
			qs:  "user=alice",
			want: func(o models.SessionOption) models.SessionOption {
				o.User = "alice"
				return o
			},
		},
		{
			msg: "an explicitly empty review status is a real filter, not the sentinel",
			qs:  "review.status=",
			want: func(o models.SessionOption) models.SessionOption {
				o.ReviewStatus = ""
				return o
			},
		},

		// A discarded strconv error used to make these silently mean 0, which
		// returned an empty page with has_next_page=true.
		{msg: "a non-numeric limit is rejected", qs: "limit=abc", wantErr: `invalid "limit" option: "abc" is not a number`},
		{msg: "a zero limit is rejected", qs: "limit=0", wantErr: `invalid "limit" option: must be at least 1`},
		{msg: "a negative limit is rejected", qs: "limit=-1", wantErr: `invalid "limit" option: must be at least 1`},
		{msg: "a non-numeric offset is rejected", qs: "offset=abc", wantErr: `invalid "offset" option: "abc" is not a number`},
		{msg: "a negative offset is rejected", qs: "offset=-1", wantErr: `invalid "offset" option: must not be negative`},
		{msg: "an offset past the maximum is rejected", qs: "offset=10001", wantErr: `invalid "offset" option: must not be greater than 10000`},
		{msg: "an unknown count mode is rejected", qs: "count=all", wantErr: `invalid "count" option: invalid count value "all"`},
		{msg: "a malformed start_date is rejected", qs: "start_date=yesterday", wantErr: `invalid "start_date" option`},
		{msg: "a malformed end_date is rejected", qs: "end_date=tomorrow", wantErr: `invalid "end_date" option`},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			qs, err := url.ParseQuery(tt.qs)
			require.NoError(t, err)

			got, err := parseSessionListOptions(qs)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)

			want := tt.want(models.NewSessionOption())
			if want.StartDate.Valid {
				assert.True(t, got.EndDate.Valid, "end_date must default to now when start_date is set")
				want.EndDate = got.EndDate
			}
			assert.Equal(t, want, got)
		})
	}
}

// Every key the handler advertises has to actually be handled, otherwise it is
// accepted on the wire and silently ignored.
func TestParseSessionListOptionsCoversEveryAdvertisedOption(t *testing.T) {
	sampleValues := map[openapi.SessionOptionKey]string{
		openapi.SessionOptionUser:                "alice",
		openapi.SessionOptionType:                "database",
		openapi.SessionOptionConnection:          "pg-prod",
		openapi.SessionOptionReviewStatus:        "PENDING",
		openapi.SessionOptionReviewApproverEmail: "a@b.c",
		openapi.SessionOptionBatchID:             "batch-1",
		openapi.SessionOptionCorrelationID:       "corr-1",
		openapi.SessionOptionStartDate:           "2026-01-01T00:00:00Z",
		openapi.SessionOptionEndDate:             "2026-01-02T00:00:00Z",
		openapi.SessionOptionLimit:               "50",
		openapi.SessionOptionOffset:              "5",
		openapi.SessionOptionJiraIssueKey:        "ABC-1",
		openapi.SessionOptionCount:               "none",
	}

	baseline := models.NewSessionOption()
	for _, optKey := range openapi.AvailableSessionOptions {
		t.Run(string(optKey), func(t *testing.T) {
			sample, ok := sampleValues[optKey]
			require.True(t, ok, "no sample value for advertised option %q", optKey)

			got, err := parseSessionListOptions(url.Values{string(optKey): []string{sample}})
			require.NoError(t, err)
			assert.NotEqual(t, baseline, got, "option %q is advertised but changes nothing", optKey)
		})
	}
}
