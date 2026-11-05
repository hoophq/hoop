package daemon

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestICAGPUConfigForwardingAndRanges(t *testing.T) {
	doc := `{"ocr_turbo_library":"/g/frame.so","ocr_turbo_det_model":"/g/det", "ocr_turbo_rec_model":"/g/rec", "ocr_turbo_rec_dict":"/g/dict", "ocr_turbo_cls_model":"/g/cls", "options":{"ocr_backend":"turboocr","ocr_turbo_device":2,"ocr_turbo_framebuffer_bytes":1073741824,"ocr_turbo_full_screen":false,"ocr_turbo_regional_cache":true,"pii_matches_only":true,"ner_backend":"ort","ner_accelerator":"cuda","ner_gpu_device":2,"ner_entities":["PERSON","LOCATION","NRP"]}}`
	var cfg ICARuntimeConfig
	if err := json.Unmarshal([]byte(doc), &cfg); err != nil {
		t.Fatal(err)
	}
	if p := cfg.validate(false, nil); len(p) != 0 {
		t.Fatal(p)
	}
	got := icaRuntimeFor(&Config{ICA: &cfg})
	if got.OCRTurboLibrary != "/g/frame.so" || got.OCRTurboDetModel != "/g/det" ||
		got.OCRTurboRecModel != "/g/rec" || got.OCRTurboRecDict != "/g/dict" ||
		got.OCRTurboClsModel != "/g/cls" || *got.Options.NERAccelerator != "cuda" ||
		!*got.Options.PIIMatchesOnly || *got.Options.OCRTurboDevice != 2 ||
		*got.Options.OCRTurboFramebufferBytes != 1<<30 {
		t.Fatal("GPU runtime forwarding lost fields")
	}
	for _, doc := range []string{
		`{"ocr_backend":"typo"}`, `{"ner_accelerator":"typo"}`,
		`{"ocr_turbo_device":-1}`, `{"ner_gpu_device":-1}`,
		`{"ner_entities":["MISC"]}`, `{"ner_entities":[""]}`,
	} {
		var o ICAOptions
		if err := json.Unmarshal([]byte(doc), &o); err != nil {
			t.Fatal(err)
		}
		if p := o.validate("ica.options"); len(p) == 0 {
			t.Fatalf("accepted %s", doc)
		}
	}
	// Paths are checked on the serving host, not during offline validation.
	problems := strings.Join(cfg.validate(true, nil), "\n")
	if !strings.Contains(problems, "ica.ocr_turbo_library:") {
		t.Fatal(problems)
	}
}
