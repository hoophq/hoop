package apijiraintegration

import "testing"

func TestBuildAssetObjectsQuery(t *testing.T) {
	for _, tt := range []struct {
		msg                string
		objectTypeID       string
		objectSchemaID     string
		name               string
		referenceObjectIDs []string
		want               string
	}{
		{
			msg:          "no reference filters",
			objectTypeID: "13",
			name:         "db",
			want:         `objectTypeId = "13" AND name LIKE "db"`,
		},
		{
			msg:            "with object schema id",
			objectTypeID:   "13",
			objectSchemaID: "7",
			name:           "db",
			want:           `objectTypeId = "13" AND objectSchemaId = "7" AND name LIKE "db"`,
		},
		{
			msg:                "single reference filter",
			objectTypeID:       "13",
			name:               "db",
			referenceObjectIDs: []string{"42"},
			want:               `objectTypeId = "13" AND name LIKE "db" AND (object HAVING outboundReferences(objectId = 42) OR object HAVING inboundReferences(objectId = 42))`,
		},
		{
			msg:                "multiple reference filters",
			objectTypeID:       "13",
			objectSchemaID:     "7",
			name:               "",
			referenceObjectIDs: []string{"42", "77"},
			want: `objectTypeId = "13" AND objectSchemaId = "7" AND name LIKE "" AND ` +
				`(object HAVING outboundReferences(objectId = 42) OR object HAVING inboundReferences(objectId = 42)) AND ` +
				`(object HAVING outboundReferences(objectId = 77) OR object HAVING inboundReferences(objectId = 77))`,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			got := buildAssetObjectsQuery(tt.objectTypeID, tt.objectSchemaID, tt.name, tt.referenceObjectIDs)
			if got != tt.want {
				t.Errorf("query mismatch\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
}
