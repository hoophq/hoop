package daemon

// ICAGPUStats is the engine's process-wide GPU gate accounting since start.
// Wait is time queued for the gate; hold is time owning it.
type ICAGPUStats struct {
	Waiting        int64         `json:"waiting"`
	Update         ICAGPUOpStats `json:"update"`
	Recognize      ICAGPUOpStats `json:"recognize"`
	RecognizeBatch ICAGPUOpStats `json:"recognize_batch"`
	Render         ICAGPUOpStats `json:"render"`
}

type ICAGPUOpStats struct {
	Calls     uint64 `json:"calls"`
	WaitNanos uint64 `json:"wait_ns"`
	HoldNanos uint64 `json:"hold_ns"`
	// Regions and Pixels count OCR crops; recognize operations only.
	Regions uint64 `json:"regions,omitempty"`
	Pixels  uint64 `json:"pixels,omitempty"`
}

// ICAGPUReporter is optional. It reports false while the engine loads and
// when its OCR backend does not use a GPU.
type ICAGPUReporter interface {
	ICAGPUStats() (ICAGPUStats, bool)
}

func icaGPUStats(endpoint endpointServer) *ICAGPUStats {
	reporter, ok := endpoint.(ICAGPUReporter)
	if !ok {
		return nil
	}

	stats, ok := reporter.ICAGPUStats()
	if !ok {
		return nil
	}

	return &stats
}
