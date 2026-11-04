package openapi

import (
	"reflect"
	"strings"
	"testing"
)

// The env vars of a review (private.reviews.input_env_vars) can hold runbook
// secrets. No review or session API type may expose them (ENG-1033).
func TestReviewAndSessionTypesDoNotExposeEnvVars(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(Review{}),
		reflect.TypeOf(SessionReview{}),
		reflect.TypeOf(Session{}),
	} {
		assertNoEnvField(t, typ, typ.Name(), map[reflect.Type]bool{})
	}
}

func assertNoEnvField(t *testing.T, typ reflect.Type, path string, seen map[reflect.Type]bool) {
	t.Helper()
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || seen[typ] {
		return
	}
	seen[typ] = true
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "-" {
			continue
		}
		name := strings.ToLower(f.Name + " " + tag)
		if strings.Contains(name, "envvar") || strings.Contains(name, "env_var") {
			t.Errorf("%s.%s exposes env vars in the API", path, f.Name)
		}
		assertNoEnvField(t, f.Type, path+"."+f.Name, seen)
	}
}
