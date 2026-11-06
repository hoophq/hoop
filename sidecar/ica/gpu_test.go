package ica

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
	engine "github.com/hoophq/libhoop-ica/ica"
)

func gpuRuntime() daemon.ICARuntime {
	return daemon.ICARuntime{
		OCRTurboLibrary: "/gpu/frame.so", OCRTurboDetModel: "/gpu/det.engine",
		OCRTurboRecModel: "/gpu/rec.engine", OCRTurboRecDict: "/gpu/dict.txt",
		OCRTurboClsModel: "/gpu/cls.engine", NERORTLibrary: "/gpu/ort.so",
		Options: daemon.ICAOptions{OCRBackend: ptr("turboocr"),
			OCRTurboDevice: ptr(1), OCRTurboFramebufferBytes: ptr(uint64(1 << 30)),
			OCRTurboRegionalCache: ptr(true), OCRTurboFullScreen: ptr(false),
			PIIMatchesOnly: ptr(true), NERBackend: ptr("go"),
			NERAccelerator: ptr("cpu"), NERGPUDevice: ptr(1),
			NEREntities: []string{"PERSON", "NRP"}},
	}
}

func TestGPUProfileAndExplicitOverrides(t *testing.T) {
	rt := gpuRuntime()
	cfg := resolveRuntimeConfig(rt)
	if cfg.OCRBackend != "turboocr" || cfg.TurboLibrary != "/gpu/frame.so" ||
		cfg.TurboDetModel != "/gpu/det.engine" || cfg.TurboRecModel != "/gpu/rec.engine" ||
		cfg.TurboRecDict != "/gpu/dict.txt" || cfg.TurboClsModel != "/gpu/cls.engine" ||
		cfg.TurboDevice != 1 || cfg.TurboFramebufferBytes != 1<<30 ||
		!cfg.TurboRegionalCache || cfg.TurboFullScreen || !cfg.PIIMatchesOnly ||
		cfg.NERORTLibrary != "/gpu/ort.so" || cfg.NERGPUDevice != 1 ||
		strings.Join(cfg.NEREntities, ",") != "PERSON,NRP" {
		t.Fatalf("GPU overrides lost: %+v", cfg)
	}
	if cfg.OCRWorkers != 1 || cfg.OCRTiles != 1 || cfg.OCRScaleFactor != 1 ||
		cfg.OCRGrayPreparation || cfg.OCRLineFallback || cfg.OCRLineBandScale ||
		cfg.OCRFreshNativeScale || cfg.OCRFreshReuseFrames != 0 || cfg.OCRThreadLimit != 0 {
		t.Fatal("implicit CPU settings survived GPU selection")
	}
	// Pure config validation succeeds with nonexistent paths and no GPU.
	// The CPU NER selection here makes this test work in non-ORT builds too.
	cfg.NERGPUDevice = 0
	if err := engine.ValidateRuntimeConfig(cfg); err != nil {
		t.Fatal(err)
	}
	rt.Options.NERGPUDevice = ptr(0)
	srv, err := Build(rt, singleLane(t))
	if err != nil {
		t.Fatal(err)
	}
	if srv.Ready() {
		t.Fatal("Build loaded a runtime")
	}
	srv.Close()
	for _, change := range []func(*daemon.ICAOptions){
		func(o *daemon.ICAOptions) { o.OCRTiles = ptr(8) },
		func(o *daemon.ICAOptions) { o.OCRWorkers = ptr(8) },
		func(o *daemon.ICAOptions) { o.OCRGrayPreparation = ptr(true) },
		func(o *daemon.ICAOptions) { o.OCRThreadLimit = ptr(1) },
		// ocr_fresh_reuse_frames is not here: match-only zeroes it before
		// the engine sees it (resolveRuntimeConfig), on CPU and GPU alike.
		func(o *daemon.ICAOptions) { o.VideoBatch = ptr(false) },
		func(o *daemon.ICAOptions) { o.OCRTurboFullScreen = ptr(true) },
	} {
		bad := rt
		change(&bad.Options)
		if err := engine.ValidateRuntimeConfig(resolveRuntimeConfig(bad)); err == nil {
			t.Fatal("explicit conflicting GPU option accepted")
		}
	}
	rt.Options.NERBackend, rt.Options.NERAccelerator, rt.Options.NERGPUDevice = ptr("ort"), ptr("cuda"), ptr(2)
	cfg = resolveRuntimeConfig(rt)
	if cfg.NERBackend != "ort" || cfg.NERAccelerator != "cuda" || cfg.NERGPUDevice != 2 {
		t.Fatal("CUDA NER lost")
	}
	if !engine.ORTCompiled && engine.ValidateRuntimeConfig(cfg) == nil {
		t.Fatal("non-ORT build accepted CUDA")
	}
	cpu := resolveRuntimeConfig(daemon.ICARuntime{})
	defaults := engine.DefaultRuntimeConfig()
	if cpu.OCRBackend != defaults.OCRBackend || cpu.OCRWorkers != defaults.OCRWorkers ||
		cpu.OCRTiles != defaults.OCRTiles || cpu.OCRGrayPreparation != defaults.OCRGrayPreparation {
		t.Fatal("CPU defaults changed")
	}
}

func TestGPUHostCheckDoesNotFallBack(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "gpu")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOOP_ICA_GPU_ASSETS", dir)
	if err := HostCheck(); err == nil || !strings.Contains(err.Error(), "GPU assets manifest") {
		t.Fatalf("missing explicit GPU assets did not fail: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	err := HostCheck()
	if err == nil || strings.Contains(err.Error(), "ocr worker") {
		t.Fatalf("GPU host checked CPU assets: %v", err)
	}
}

func TestGPUHostDiscoveryNeedsNoCPUAssets(t *testing.T) {
	if !engine.ORTCompiled {
		t.Skip("GPU host discovery requires ORT")
	}
	base := t.TempDir()
	root := filepath.Join(base, "gpu")
	t.Setenv("HOOP_ICA_GPU_ASSETS", root)
	for _, rel := range []string{"gpu/manifest.json", "gpu/lib/libhoop-gpuframe.so", "gpu/lib/libturboocr-device.so",
		"gpu/engines/det.engine", "gpu/engines/rec.engine", "gpu/engines/dict.txt",
		"gpu/ort/lib/libonnxruntime.so", "gpu/ort/lib/libonnxruntime_providers_cuda.so",
		"gpu/ort/lib/libonnxruntime_providers_shared.so", "models/KnightsAnalytics_distilbert-NER/config.json"} {
		path := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("discovery fixture; not inference"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := HostCheck(); err != nil {
		t.Fatal(err)
	}
	cfg := resolveRuntimeConfig(daemon.ICARuntime{Options: daemon.ICAOptions{NEREntities: []string{}}})
	if len(cfg.NEREntities) != 4 {
		t.Fatal("empty NER selection did not inherit the profile")
	}
}
