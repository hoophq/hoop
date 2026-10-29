package alcatraz

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hoophq/alcatraz/analyzer"
	"github.com/hoophq/alcatraz/entities"
	"github.com/hoophq/alcatraz/ner"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// fakeNLP stands in for the model: it tags every occurrence of each name
// in people as PERSON and of each place in places as LOCATION.
type fakeNLP struct {
	people, places []string
	calls          atomic.Int32
}

func (f *fakeNLP) ProcessText(text, _ string) (*analyzer.NlpArtifacts, error) {
	f.calls.Add(1)
	art := &analyzer.NlpArtifacts{}
	tag := func(words []string, entity string) {
		for _, w := range words {
			for at := 0; ; {
				i := strings.Index(text[at:], w)
				if i < 0 {
					break
				}
				art.Ents = append(art.Ents, analyzer.NerSpan{EntityType: entity, Start: at + i, End: at + i + len(w), Score: 0.95})
				at += i + len(w)
			}
		}
	}
	tag(f.people, entities.Person)
	tag(f.places, entities.Location)
	return art, nil
}

func (f *fakeNLP) Recognizer(language string) analyzer.Recognizer {
	return &fakeRecognizer{nlp: f, lang: language}
}

// fakeRecognizer mirrors ner.Recognizer: Analyze runs the model itself,
// AnalyzeWithArtifacts reads the engine's shared pass.
type fakeRecognizer struct {
	nlp  *fakeNLP
	lang string
}

func (r *fakeRecognizer) Name() string { return "FakeNER" }
func (r *fakeRecognizer) SupportedEntities() []string {
	return []string{entities.Person, entities.Location, Organization, entities.NRP}
}
func (r *fakeRecognizer) SupportedLanguage() string { return r.lang }
func (r *fakeRecognizer) Analyze(text string, ents []string) []analyzer.Result {
	art, _ := r.nlp.ProcessText(text, r.lang)
	return r.AnalyzeWithArtifacts(text, ents, art)
}
func (r *fakeRecognizer) AnalyzeWithArtifacts(_ string, ents []string, art *analyzer.NlpArtifacts) []analyzer.Result {
	var out []analyzer.Result
	for _, e := range art.Ents {
		if ents != nil && !slices.Contains(ents, e.EntityType) {
			continue
		}
		out = append(out, analyzer.Result{EntityType: e.EntityType, Start: e.Start, End: e.End, Score: e.Score, RecognizerName: r.Name()})
	}
	return out
}

// withFakeNLP routes every load through fake and empties the engine cache
// for the test, so each test observes its own loads.
func withFakeNLP(t *testing.T, fake *fakeNLP, loadErr error) (loads *atomic.Int32) {
	t.Helper()
	loads = new(atomic.Int32)
	prev := newNLPEngine
	newNLPEngine = func(NER, string) (nlpEngine, error) {
		loads.Add(1)
		if loadErr != nil {
			return nil, loadErr
		}
		return fake, nil
	}
	nerEngines.Lock()
	prevCache := nerEngines.byConfig
	nerEngines.byConfig = map[NER]nlpEngine{}
	nerEngines.Unlock()
	t.Cleanup(func() {
		newNLPEngine = prev
		nerEngines.Lock()
		nerEngines.byConfig = prevCache
		nerEngines.Unlock()
	})
	return loads
}

func goNER() *NER { return &NER{Backend: ner.BackendGo, ModelsDir: "/nonexistent/models"} }

func TestNEREntitiesRefusedWithoutTheBlock(t *testing.T) {
	for _, lists := range []Options{
		{Entities: []string{entities.Person}},
		{Ignored: []string{Organization}},
	} {
		_, err := NewDetector(lists)
		if err == nil {
			t.Fatalf("%+v: PERSON accepted without a NER block", lists)
		}
		if !strings.Contains(err.Error(), "pii.ner") {
			t.Errorf("the refusal must say what to set: %v", err)
		}
	}
	// A plain typo gets no NER hint.
	_, err := NewDetector(Options{Entities: []string{"US_SSSN"}})
	if err == nil || strings.Contains(err.Error(), "pii.ner") {
		t.Errorf("a typo is not a missing NER block: %v", err)
	}
}

func TestNERBlockAddsTheFourEntities(t *testing.T) {
	withFakeNLP(t, &fakeNLP{}, nil)
	d, err := NewDetector(Options{NER: goNER()})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range NEREntities() {
		if !d.claims(e) {
			t.Errorf("%s missing from the active set", e)
		}
	}
	d, err = NewDetector(Options{NER: goNER(), Entities: []string{entities.Person, entities.USSSN}})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Entities(); !slices.Equal(got, []string{entities.Person, entities.USSSN}) {
		t.Errorf("Entities = %v", got)
	}
}

func TestNERMasksAndScansNames(t *testing.T) {
	fake := &fakeNLP{people: []string{"Alice Johnson"}, places: []string{"Lisbon"}}
	withFakeNLP(t, fake, nil)
	d, err := NewDetector(Options{NER: goNER(), Entities: []string{entities.Person, entities.Location, entities.EmailAddress}})
	if err != nil {
		t.Fatal(err)
	}

	m, err := NewMasker(d, []Rule{{Entity: entities.Person, Strategy: StrategyRedact}})
	if err != nil {
		t.Fatal(err)
	}
	out, res := m.MaskString("Alice Johnson <alice@example.com> moved to Lisbon")
	if strings.Contains(out, "Alice Johnson") {
		t.Errorf("name survived masking: %q", out)
	}
	if !strings.Contains(out, "Lisbon") || !strings.Contains(out, "alice@example.com") {
		t.Errorf("a rule naming PERSON masked more than PERSON: %q", out)
	}
	if !slices.Equal(res.Entities, []string{entities.Person}) {
		t.Errorf("Entities = %v", res.Entities)
	}

	got := d.ScanText("Alice Johnson moved to Lisbon")
	if !slices.Equal(got, []string{entities.Location, entities.Person}) {
		t.Errorf("ScanText = %v", got)
	}
	if got := d.ScanTextFor([]string{entities.Location}, "Alice Johnson moved to Lisbon"); !slices.Equal(got, []string{entities.Location}) {
		t.Errorf("ScanTextFor = %v", got)
	}
	if red, _ := d.RedactText("Alice Johnson moved to Lisbon"); red != "<PERSON> moved to <LOCATION>" {
		t.Errorf("RedactText = %q", red)
	}
	if fake.calls.Load() == 0 {
		t.Error("the fake model was never run")
	}
}

func TestNERGuardrailDeniesAStatementCarryingAName(t *testing.T) {
	withFakeNLP(t, &fakeNLP{people: []string{"Alice Johnson"}}, nil)
	d, err := NewDetector(Options{NER: goNER()})
	if err != nil {
		t.Fatal(err)
	}
	rules, err := policy.NewRulesWithScanner([]policy.Rule{{
		Name: "no-names", Type: policy.MatchPII, Entities: []string{entities.Person},
	}}, d)
	if err != nil {
		t.Fatal(err)
	}
	stmt := inspect.Statement{Protocol: inspect.Postgres, Direction: inspect.FromClient, Operation: inspect.OpSelect,
		Text: "SELECT * FROM people WHERE name = 'Alice Johnson'"}
	v := rules.Evaluate(stmt)
	if !v.Denied || !strings.Contains(v.Message, entities.Person) || strings.Contains(v.Message, "Alice") {
		t.Errorf("verdict = %+v", v)
	}
	stmt.Text = "SELECT * FROM people WHERE id = 1"
	if v := rules.Evaluate(stmt); v.Denied {
		t.Errorf("clean statement denied: %+v", v)
	}
}

func TestNEREngineIsLoadedOncePerBlock(t *testing.T) {
	loads := withFakeNLP(t, &fakeNLP{}, nil)
	for range 3 {
		if _, err := NewDetector(Options{NER: goNER()}); err != nil {
			t.Fatal(err)
		}
	}
	if n := loads.Load(); n != 1 {
		t.Errorf("the same block loaded the model %d times", n)
	}
	other := goNER()
	other.ModelsDir = "/elsewhere"
	if _, err := NewDetector(Options{NER: other}); err != nil {
		t.Fatal(err)
	}
	if n := loads.Load(); n != 2 {
		t.Errorf("a different block must load its own engine, loads = %d", n)
	}
}

func TestNERLoadFailureRefusesTheDetector(t *testing.T) {
	withFakeNLP(t, nil, errors.New("offline: model.onnx: sha256 mismatch"))
	_, err := NewDetector(Options{NER: goNER()})
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("the load error must reach the caller: %v", err)
	}
}

func TestNERResolveDefaults(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "libonnxruntime.so")
	if err := os.WriteFile(lib, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := NER{Backend: "go"}.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != (NER{ModelsDir: DefaultModelsDir, Backend: ner.BackendGo}) {
		t.Errorf("go resolve = %+v", got)
	}
	if _, err := (NER{Backend: "xla"}).resolve(); err == nil {
		t.Error("xla accepted")
	}
	if _, err := (NER{}).resolve(); ORTCompiled && err == nil {
		t.Error("the ort default must refuse a missing library")
	}

	ort, err := NER{Backend: " ORT ", ORTLibrary: lib}.resolve()
	if !ORTCompiled {
		if err == nil || !strings.Contains(err.Error(), "-tags ORT") {
			t.Fatalf("ort on a build without the tag must name the tag: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if ort != (NER{ModelsDir: DefaultModelsDir, Backend: ner.BackendORT, ORTLibrary: lib, Threads: defaultNERThreads}) {
		t.Errorf("ort resolve = %+v", ort)
	}
	if _, err := (NER{Backend: "ort", ORTLibrary: lib, Threads: 17}).resolve(); err == nil {
		t.Error("threads 17 accepted")
	}
	if _, err := (NER{Backend: "ort", ORTLibrary: filepath.Join(t.TempDir(), "missing.so")}).resolve(); err == nil {
		t.Error("a missing library accepted")
	}
}

func TestPluginFromConfigDecodesTheNERBlock(t *testing.T) {
	withFakeNLP(t, &fakeNLP{people: []string{"Alice Johnson"}}, nil)
	p, err := PluginFromConfig([]byte(`{"entities": ["PERSON"], "ner": {"backend": "go", "models_dir": "/m"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.ScanText("Alice Johnson"); !slices.Equal(got, []string{entities.Person}) {
		t.Errorf("ScanText = %v", got)
	}
	if _, err := PluginFromConfig([]byte(`{"ner": {"backend": "tpu"}}`)); err == nil || !strings.Contains(err.Error(), "backend") {
		t.Errorf("a bad backend must be refused by name: %v", err)
	}
}

// TestNERRealModel loads the pinned model. HOOP_ALCATRAZ_MODELS_DIR names
// the models directory; under -tags ORT, HOOP_ALCATRAZ_ORT_LIBRARY names
// libonnxruntime. CI runs it with the files fetched (pullrequest.yml).
func TestNERRealModel(t *testing.T) {
	dir := os.Getenv("HOOP_ALCATRAZ_MODELS_DIR")
	if dir == "" {
		t.Skip("HOOP_ALCATRAZ_MODELS_DIR is unset")
	}
	n := &NER{ModelsDir: dir, Backend: ner.BackendGo}
	if ORTCompiled {
		n.Backend = ner.BackendORT
		n.ORTLibrary = os.Getenv("HOOP_ALCATRAZ_ORT_LIBRARY")
	}
	d, err := NewDetector(Options{NER: n, Entities: []string{entities.Person, entities.Location, Organization, entities.NRP}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMasker(d, []Rule{{Entity: entities.Person, Strategy: StrategyRedact}})
	if err != nil {
		t.Fatal(err)
	}
	// What the relay hands the masker: a result-set cell on its own, and an
	// HTTP body. A bare tab-separated row is neither, and the model reads
	// it worse: context-sensitive NER wants the key beside the value.
	cell, found, count := m.MaskCell("name", []byte("Maria Fernanda Souza"))
	if string(cell) != "[REDACTED:PERSON]" || !slices.Equal(found, []string{entities.Person}) || count != 1 {
		t.Errorf("MaskCell = %q, %v, %d", cell, found, count)
	}
	body := `{"name":"John Smith","city":"Berlin","org":"Acme Corp"}`
	out, res := m.MaskString(body)
	if out != `{"name":"[REDACTED:PERSON]","city":"Berlin","org":"Acme Corp"}` {
		t.Errorf("MaskString = %q", out)
	}
	if !slices.Equal(res.Entities, []string{entities.Person}) {
		t.Errorf("Entities = %v", res.Entities)
	}
	if got := d.ScanText(body); !slices.Equal(got, []string{entities.Location, Organization, entities.Person}) {
		t.Errorf("ScanText = %v", got)
	}
	if got := d.ScanTextFor([]string{entities.Location}, "John Smith moved to Berlin"); !slices.Equal(got, []string{entities.Location}) {
		t.Errorf("ScanTextFor LOCATION = %v", got)
	}
}
