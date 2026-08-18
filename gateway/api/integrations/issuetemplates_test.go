package apijiraintegration

import (
	"fmt"
	"testing"
)

func TestBuildAssetObjectsQuery(t *testing.T) {
	for _, tt := range []struct {
		msg            string
		objectTypeID   string
		objectSchemaID string
		name           string
		aql            string
		want           string
	}{
		{
			msg:          "no schema, no aql",
			objectTypeID: "1",
			name:         "srv",
			want:         `objectTypeId = "1" AND name LIKE "srv"`,
		},
		{
			msg:            "with schema",
			objectTypeID:   "1",
			objectSchemaID: "9",
			name:           "srv",
			want:           `objectTypeId = "1" AND objectSchemaId = "9" AND name LIKE "srv"`,
		},
		{
			msg:          "with aql only",
			objectTypeID: "1",
			name:         "srv",
			aql:          `"Status" = "Active"`,
			want:         `("Status" = "Active") AND objectTypeId = "1" AND name LIKE "srv"`,
		},
		{
			msg:            "with schema and aql",
			objectTypeID:   "1",
			objectSchemaID: "9",
			name:           "srv",
			aql:            `"Status" = "Active"`,
			want:           `("Status" = "Active") AND objectTypeId = "1" AND objectSchemaId = "9" AND name LIKE "srv"`,
		},
		{
			msg:          "empty name keeps LIKE filter",
			objectTypeID: "1",
			want:         `objectTypeId = "1" AND name LIKE ""`,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			got := buildAssetObjectsQuery(tt.objectTypeID, tt.objectSchemaID, tt.name, tt.aql)
			if got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestParseObjectTypeIDs(t *testing.T) {
	for _, tt := range []struct {
		msg     string
		raw     string
		want    []string
		wantErr string
	}{
		{msg: "single id", raw: "76", want: []string{"76"}},
		{msg: "trims and drops blanks", raw: " 76 , ,77,", want: []string{"76", "77"}},
		{msg: "dedupes repeated ids", raw: "77,77,76,77", want: []string{"77", "76"}},
		{msg: "empty is rejected", raw: "", wantErr: "object_type_ids query string is required"},
		{msg: "blanks only is rejected", raw: " , ,", wantErr: "object_type_ids query string is required"},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			got, err := parseObjectTypeIDs(tt.raw)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("got err %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("got  %v\nwant %v", got, tt.want)
			}
		})
	}
}
