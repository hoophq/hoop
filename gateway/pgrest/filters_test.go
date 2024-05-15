package pgrest

import (
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestEqFilter(t *testing.T) {
	for _, tt := range []struct {
		msg    string
		params url.Values
		want   string
	}{
		{
			msg:    "it should encode single param to equal filter",
			params: url.Values{"name": []string{"oliva"}},
			want:   "name=eq.oliva",
		},
		{
			msg:    "it should encode multiple params to equal filter",
			params: url.Values{"name": []string{"oliva"}, "mode": []string{"standard"}},
			want:   "name=eq.oliva&mode=eq.standard",
		},
		{
			msg:    "it should return empty string when params are empty",
			params: url.Values{},
			want:   "",
		},
		{
			msg:    "it should ignore empty values when filtering",
			params: url.Values{"hostname": []string{""}, "name": []string{"john-doe"}},
			want:   "name=eq.john-doe",
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			got := WithEqFilter(tt.params).Encode()
			if !cmp.Equal(got, tt.want) {
				t.Errorf("fail to encode filter, got=%v, want=%v", got, tt.want)
			}
		})
	}

}
