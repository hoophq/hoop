package apijiraintegration

import "testing"

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
