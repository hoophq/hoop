package analyzer

// The model Hoop hosts for sidecars without an LLM of their own (DEP-161).
// The analyzer/hoop provider calls it; the daemon reports these values when
// the config's use_hoop_llm_provider leaves model and endpoint empty.
const (
	// HostedEndpoint is the hosted model. Every sidecar binary carries this
	// URL, and old ones are never upgraded, so the name must keep resolving.
	HostedEndpoint = "https://ai-session-analyzer-default.hoop.dev/v1/chat/completions"

	// HostedModel is the model the hosted server loads. llama-server serves
	// one model and ignores the name; it is sent for logs and audit records.
	HostedModel = "qwen3-1.7b"
)
