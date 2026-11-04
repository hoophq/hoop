package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
	engine "github.com/hoophq/libhoop-ica/ica"
)

func TestICAGPUSamples(t *testing.T) {
	if !engine.ORTCompiled {
		t.Skip("GPU examples require -tags ORT")
	}
	_, here, _, _ := runtime.Caller(0)
	examples := filepath.Join(filepath.Dir(here), "../../deploy/ica-sidecar/examples")
	samples, err := filepath.Glob(filepath.Join(examples, "config.gpu.*.yaml"))
	if err != nil || len(samples) != 2 {
		t.Fatalf("GPU samples: %v, %v", samples, err)
	}
	// The Helm values example carries the same document under its `config`
	// key, with admin.listen on 0.0.0.0 for the kubelet's probes.
	samples = append(samples, filepath.Join(examples, "gpu/values.yaml"))
	for _, sample := range samples {
		t.Run(filepath.Base(filepath.Dir(sample))+"/"+filepath.Base(sample), func(t *testing.T) {
			data, err := os.ReadFile(sample)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := configyaml.ToJSON(data)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(sample, "values.yaml") {
				var values struct {
					Config json.RawMessage `json:"config"`
				}
				if err := json.Unmarshal(raw, &values); err != nil || len(values.Config) == 0 {
					t.Fatalf("helm values without a config document: %v", err)
				}
				raw = values.Config
				var admin struct {
					Admin struct{ Listen string } `json:"admin"`
				}
				if err := json.Unmarshal(raw, &admin); err != nil || admin.Admin.Listen != "0.0.0.0:19000" {
					t.Fatalf("the chart probes admin on 0.0.0.0:19000, got %q (%v)", admin.Admin.Listen, err)
				}
			}
			if err := daemon.CheckConfigBytes(raw); err != nil {
				t.Fatalf("offline sample validation: %v", err)
			}
			var cfg daemon.Config
			if err := json.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.ICA == nil || cfg.ICA.Options == nil {
				t.Fatal("missing GPU profile")
			}
			o := cfg.ICA.Options
			if *o.OCRBackend != "turboocr" || *o.NERAccelerator != "cuda" ||
				!*o.PIIMatchesOnly || !*o.OCRTurboRegionalCache {
				t.Fatal("wrong sample policy/backend")
			}
			// Host file checks are intentionally retained. Materialize dummy
			// assets here; validation must not dlopen or run any of them.
			root := t.TempDir()
			for _, path := range []*string{&cfg.ICA.OCRTurboLibrary, &cfg.ICA.OCRTurboDetModel,
				&cfg.ICA.OCRTurboRecModel, &cfg.ICA.OCRTurboRecDict, &cfg.ICA.NERORTLibrary} {
				if !strings.HasPrefix(*path, "/opt/libhoop-ica/current/gpu/") {
					t.Fatalf("not bundle-owned: %s", *path)
				}
				*path = filepath.Join(root, filepath.Base(*path))
				if err := os.WriteFile(*path, []byte("not a loadable library or model"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cfg.ICA.NERModel = filepath.Join(root, "ner")
			if err := os.Mkdir(cfg.ICA.NERModel, 0700); err != nil {
				t.Fatal(err)
			}
			raw, err = json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := daemon.LoadConfigBytes(raw)
			if err != nil {
				t.Fatal(err)
			}
			lanes, err := daemon.Validate(loaded, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(lanes) != 1 {
				t.Fatal("wrong lane count")
			}
		})
	}
}
