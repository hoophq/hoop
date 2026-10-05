package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/policy"
)

// previousReleaseTreeEnv names a checkout of the previous release and
// previousReleaseEnv its tag. make test-sidecar-compat sets both; a unit run
// skips the cross-release test.
const (
	previousReleaseTreeEnv = "SIDECAR_PREVIOUS_RELEASE_TREE"
	previousReleaseEnv     = "SIDECAR_PREVIOUS_RELEASE"
)

// The previous release must load what this plane serves (EVL-338): a 1.197
// sidecar crash-looped on a google_identity key it did not declare. Two
// documents go through ServedForm and into that release's decoder:
//
//   - the skeleton allocates every block and leaves every value zero, so a
//     field that serves its zero value fails;
//   - the full document sets every field, so a new field the plane can
//     serve without a cap tag fails.
//
// A cap field the plane refuses to that build (CheckServable) is left out
// of both.
func TestThePreviousReleaseDecodesTheServedDocument(t *testing.T) {
	tree := os.Getenv(previousReleaseTreeEnv)
	if tree == "" {
		t.Skipf("%s is not set; make test-sidecar-compat sets it", previousReleaseTreeEnv)
	}
	release := os.Getenv(previousReleaseEnv)
	if _, ok := parseRelease(release); !ok {
		t.Fatalf("%s is %q, not the release %s holds", previousReleaseEnv, release, tree)
	}
	decoder := buildPreviousDecoder(t, tree)
	out, err := exec.Command(decoder, "capabilities").Output()
	if err != nil {
		t.Fatalf("the previous release did not report its capabilities: %v", err)
	}
	hs := Handshake{Version: release, Capabilities: ParseCapabilities(strings.TrimSpace(string(out)))}
	for _, full := range []bool{false, true} {
		doc := servedCompatDocument(t, full, hs)
		cmd := exec.Command(decoder)
		cmd.Stdin = bytes.NewReader(doc)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("the previous release refuses the %s document: %s"+
				"a new field needs a cap tag, so the plane refuses it to that release (sidecar/CLAUDE.md)\n%s",
				docName(full), out, doc)
		}
	}
}

// The documents load in this build too, so a failure against the previous
// release is the release's, never the generator's.
func TestTheCompatDocumentsDecodeInThisBuild(t *testing.T) {
	for _, full := range []bool{false, true} {
		dec := json.NewDecoder(bytes.NewReader(servedCompatDocument(t, full, Handshake{Capabilities: SidecarCapabilities()})))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&Config{}); err != nil {
			t.Errorf("this build refuses its own %s document: %v", docName(full), err)
		}
	}
}

// The full document leaves no field unset, so a field the filler cannot
// reach fails here instead of passing the cross-release test unseen.
func TestTheFullCompatDocumentSetsEveryField(t *testing.T) {
	cfg, err := compatDocument(true, Handshake{Capabilities: SidecarCapabilities()})
	if err != nil {
		t.Fatal(err)
	}
	walkConfigValue(reflect.ValueOf(cfg), "the configuration", "",
		func(where, path string, f jsonField, v reflect.Value) {
			if v.IsZero() {
				t.Errorf("%s: %s is unset", where, path)
			}
		})
}

func docName(full bool) string {
	if full {
		return "full"
	}
	return "skeleton"
}

// servedCompatDocument is the JSON the plane serves for compatDocument, after
// CheckServable accepts it for the build behind hs.
func servedCompatDocument(t *testing.T, full bool, hs Handshake) []byte {
	t.Helper()
	cfg, err := compatDocument(full, hs)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckServable(cfg, hs); err != nil {
		t.Fatalf("the plane would not serve the %s document: %v", docName(full), err)
	}
	raw, err := json.Marshal(ServedForm(cfg))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// compatDocument fills every field the plane serves to the build behind hs:
// a cap field only when CheckServable grants it. The values mean nothing: only the
// decoder reads the document, never Validate. Rule types and protocols are
// ones that build lists, as CheckServable requires.
func compatDocument(full bool, hs Handshake) (Config, error) {
	var cfg Config
	since := capabilitySince()
	granted := func(c string) bool { return hs.grants(c, since) }
	if err := fillValue(reflect.ValueOf(&cfg).Elem(), full, granted, nil); err != nil {
		return Config{}, err
	}
	ruleType, protocol := firstWithPrefix(hs.Capabilities, capabilityRulePrefix), firstWithPrefix(hs.Capabilities, capabilityProtocolPrefix)
	if ruleType == "" || protocol == "" {
		return Config{}, fmt.Errorf("capabilities %v list no rule type or no protocol", hs.Capabilities)
	}
	forEachRuleSet(cfg, func(_ string, rules []policy.Rule) {
		for i := range rules {
			rules[i].Type = policy.MatchType(ruleType)
		}
	})
	for i := range cfg.Listeners {
		cfg.Listeners[i].Protocol = protocol
	}
	return cfg, nil
}

func firstWithPrefix(caps []string, prefix string) string {
	for _, c := range caps {
		if v, ok := strings.CutPrefix(c, prefix); ok {
			return v
		}
	}
	return ""
}

// fillValue allocates every struct v reaches and, when full, sets every
// leaf. A pointer to a scalar is a set value, so only full allocates it. A
// struct type already on the path is left zero, as walkConfigType leaves it.
func fillValue(v reflect.Value, full bool, granted func(string) bool, stack []reflect.Type) error {
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		if slices.Contains(stack, t) {
			return nil
		}
		stack = append(stack, t)
		for i := range t.NumField() {
			sf := t.Field(i)
			name, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
			if name == "-" || (!sf.IsExported() && !sf.Anonymous) {
				continue
			}
			if c := sf.Tag.Get("cap"); c != "" && !granted(c) {
				continue
			}
			if err := fillValue(v.Field(i), full, granted, stack); err != nil {
				return fmt.Errorf("%s.%s: %w", t.Name(), sf.Name, err)
			}
		}
	case reflect.Pointer:
		if structOf(v.Type()) == nil && !full {
			return nil
		}
		if st := v.Type().Elem(); st.Kind() == reflect.Struct && slices.Contains(stack, st) {
			return nil
		}
		v.Set(reflect.New(v.Type().Elem()))
		return fillValue(v.Elem(), full, granted, stack)
	case reflect.Slice:
		if v.Type() == reflect.TypeFor[json.RawMessage]() {
			if full {
				v.SetBytes([]byte(`{}`))
			}
			return nil
		}
		if structOf(v.Type()) == nil && !full {
			return nil
		}
		el := reflect.New(v.Type().Elem()).Elem()
		if err := fillValue(el, full, granted, stack); err != nil {
			return err
		}
		v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), el))
	case reflect.Map:
		if structOf(v.Type()) == nil && !full {
			return nil
		}
		if v.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("map key %s is not a string", v.Type().Key())
		}
		el := reflect.New(v.Type().Elem()).Elem()
		if err := fillValue(el, full, granted, stack); err != nil {
			return err
		}
		v.Set(reflect.MakeMap(v.Type()))
		v.SetMapIndex(reflect.ValueOf("k").Convert(v.Type().Key()), el)
	case reflect.String:
		if full {
			v.SetString("x")
		}
	case reflect.Bool:
		if full {
			v.SetBool(true)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if full {
			v.SetInt(1)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if full {
			v.SetUint(1)
		}
	case reflect.Float32, reflect.Float64:
		if full {
			v.SetFloat(1)
		}
	default:
		// Loud, so a new kind of field is never silently left off the wire.
		return fmt.Errorf("cannot fill a %s", v.Kind())
	}
	return nil
}

// previousDecoderSource decodes stdin the way a sidecar decodes the served
// document, or prints the header it sends. It compiles against the previous
// release, so it names only exported API.
const previousDecoderSource = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/hoophq/hoop/sidecar/daemon"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "capabilities" {
		fmt.Println(strings.Join(daemon.SidecarCapabilities(), ","))
		return
	}
	dec := json.NewDecoder(os.Stdin)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&daemon.Config{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
`

// buildPreviousDecoder compiles previousDecoderSource inside tree, a
// checkout of the previous release, against that release's go.work.
func buildPreviousDecoder(t *testing.T, tree string) string {
	t.Helper()
	module := filepath.Join(tree, "sidecar")
	dir := filepath.Join(module, "zz_compatdecode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(previousDecoderSource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "decode")
	cmd := exec.Command("go", "build", "-o", bin, "./zz_compatdecode")
	cmd.Dir = module
	cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(tree, "go.work"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the decoder in %s: %v\n%s", tree, err, out)
	}
	return bin
}
