package alcatraz

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/hoophq/alcatraz/analyzer"
	"github.com/hoophq/alcatraz/entities"
	"github.com/hoophq/alcatraz/ner"
	"github.com/knights-analytics/hugot/options"
)

// Default paths of the NER model and ONNX Runtime, the layout of the
// hoopsidecar -alcatraz and hoopagent -alcatraz images (Dockerfile.sidecar,
// Dockerfile.agent). The -ica image holds the same model under
// /opt/libhoop-ica/releases/image/models.
const (
	DefaultModelsDir  = "/opt/alcatraz/models"
	DefaultORTLibrary = "/opt/alcatraz/ort/releases/1.28.0/lib/libonnxruntime.so"
)

// ORT intra-op threads per inference. DistilBERT on one line of output
// gains nothing past two, and a relay runs one inference per masked value
// across every connection, so ORT's all-cores default oversubscribes.
const (
	defaultNERThreads = 2
	maxNERThreads     = 16
)

// Organization is the entity type the NER model emits for a company or an
// institution. The entities package has no constant for it: alcatraz drops it
// by default for its false positives on product and vendor names.
const Organization = "ORGANIZATION"

// NEREntities names the entity types only the NER model detects, sorted.
// Without a NER block, NewDetector refuses each of them by name.
func NEREntities() []string {
	return []string{entities.Location, entities.NRP, Organization, entities.Person}
}

// NER configures the statistical recognizers: an ONNX token-classification
// model (alcatraz's pinned KnightsAnalytics/distilbert-NER) that detects
// PERSON, LOCATION, ORGANIZATION and NRP. The zero value of each field is
// its default; see the constants above.
//
// A loaded engine holds hundreds of MB and is shared by every Detector built
// over the same NER for the life of the process. It is never closed: the
// daemon rebuilds the Detector on a reload and a running connection may
// still hold the previous one, so a changed block loads a second engine
// beside the first until restart.
type NER struct {
	// ModelsDir is the parent directory holding one subdirectory per model
	// id, what "alcatraz models download --dest <dir>" writes. The model
	// is never fetched: every pinned file is verified against its sha256
	// at load, and a missing or altered one is an error naming the file.
	ModelsDir string
	// Backend is ner.BackendGo or ner.BackendORT. Empty selects ort in a
	// build compiled with -tags ORT and go otherwise; ort in a build
	// without the tag is an error, never a fallback.
	Backend string
	// ORTLibrary is libonnxruntime.so, read by the ort backend only.
	ORTLibrary string
	// Threads is the ORT intra-op thread count per inference, 1..16. The
	// go backend ignores it.
	Threads int
}

// resolve fills the defaults and refuses what this build cannot run. The
// result is the engine cache key, so every spelling of one setup maps to
// one engine.
func (n NER) resolve() (NER, error) {
	if n.ModelsDir == "" {
		n.ModelsDir = DefaultModelsDir
	}
	n.Backend = strings.ToLower(strings.TrimSpace(n.Backend))
	if n.Backend == "" {
		n.Backend = defaultNERBackend
	}
	switch n.Backend {
	case ner.BackendGo:
		n.ORTLibrary = ""
		n.Threads = 0
	case ner.BackendORT:
		if !ORTCompiled {
			return NER{}, fmt.Errorf("backend ort is not compiled into this build; build with CGO_ENABLED=1 -tags ORT, or set backend: go")
		}
		if n.ORTLibrary == "" {
			n.ORTLibrary = DefaultORTLibrary
		}
		if _, err := os.Stat(n.ORTLibrary); err != nil {
			return NER{}, fmt.Errorf("ort_library: %w", err)
		}
		if n.Threads == 0 {
			n.Threads = defaultNERThreads
		}
		if n.Threads < 1 || n.Threads > maxNERThreads {
			return NER{}, fmt.Errorf("threads %d is outside 1..%d", n.Threads, maxNERThreads)
		}
	default:
		return NER{}, fmt.Errorf("unknown backend %q; use go or ort", n.Backend)
	}
	return n, nil
}

// nlpEngine is what NewDetector attaches: the model pass the analyzer runs
// once per Analyze, and the recognizer that reads its spans. *ner.Engine
// satisfies it through nerEngine; a test substitutes a fake.
type nlpEngine interface {
	analyzer.NlpEngine
	Recognizer(language string) analyzer.Recognizer
}

// nerEngine adapts *ner.Engine: its Recognizer returns the concrete type.
type nerEngine struct{ *ner.Engine }

func (e nerEngine) Recognizer(language string) analyzer.Recognizer {
	return e.Engine.Recognizer(language)
}

// newNLPEngine loads the model described by a resolved NER. A test replaces
// it; the cache in loadNER stays in front of either.
var newNLPEngine = func(n NER, lang string) (nlpEngine, error) {
	cfg := ner.DefaultConfig()
	cfg.ModelsDir = n.ModelsDir
	cfg.Offline = true
	cfg.Backend = n.Backend
	cfg.ORTLibraryPath = n.ORTLibrary
	// Query results and HTTP bodies, not prose: a wide table fed in as one
	// sequence drives recall to zero on names the model finds in isolation.
	cfg.Segmentation = ner.SegmentLines
	// DefaultConfig drops ORGANIZATION; a rule that names it wants it.
	cfg.LabelsToIgnore = []string{"MISC"}
	if n.Backend == ner.BackendORT {
		cfg.SessionOptions = []options.WithOption{
			options.WithIntraOpNumThreads(n.Threads),
			options.WithInterOpNumThreads(1),
		}
	}
	eng, err := ner.New(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	// A model that loads and then fails its first inference fails here,
	// at startup, not on the first response.
	if _, err := eng.ProcessText("Hoop NER warmup.", lang); err != nil {
		_ = eng.Close()
		return nil, fmt.Errorf("warmup: %w", err)
	}
	return nerEngine{eng}, nil
}

// nerEngines is the process-wide cache, one engine per resolved NER.
var nerEngines = struct {
	sync.Mutex
	byConfig map[NER]nlpEngine
}{byConfig: map[NER]nlpEngine{}}

// loadNER returns the engine for n, loading it on first use.
func loadNER(n NER, lang string) (nlpEngine, error) {
	n, err := n.resolve()
	if err != nil {
		return nil, err
	}
	nerEngines.Lock()
	defer nerEngines.Unlock()
	if eng, ok := nerEngines.byConfig[n]; ok {
		return eng, nil
	}
	slog.Info("alcatraz: loading the NER model",
		"models_dir", n.ModelsDir, "backend", n.Backend, "ort_library", n.ORTLibrary, "threads", n.Threads)
	eng, err := newNLPEngine(n, lang)
	if err != nil {
		return nil, err
	}
	supported := eng.Recognizer(lang).SupportedEntities()
	sort.Strings(supported)
	slog.Info("alcatraz: NER model loaded", "backend", n.Backend, "entities", strings.Join(supported, ","))
	nerEngines.byConfig[n] = eng
	return eng, nil
}
