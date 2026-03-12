(ns webapp.features.ai-session-analyzer.constants)

(def providers
  [{:id "azure-openai"
    :label "Azure Open AI"
    :logo "/images/azure-logo.svg"}
   {:id "anthropic"
    :label "Anthropic"
    :logo "/images/anthropic-logo.svg"}
   {:id "openai"
    :label "OpenAI"
    :logo "/images/openai-logo.svg"}
   {:id "custom"
    :label "Custom"
    :logo nil}])

(def models-by-provider
  {"openai" [;; GPT-5.x frontier
             "gpt-5.2-pro"
             "gpt-5.2"
             "gpt-5.2-codex"
             "gpt-5.3-codex"
             ;; GPT-5
             "gpt-5"
             "gpt-5-pro"
             "gpt-5-mini"
             "gpt-5-nano"
             "gpt-5-codex"
             ;; GPT-4.1
             "gpt-4.1"
             "gpt-4.1-mini"
             "gpt-4.1-nano"
             ;; o-series
             "o3-pro"
             "o3"
             "o4-mini"
             "o3-mini"
             "o1"
             "codex-mini-latest"
             ;; GPT-4o
             "gpt-4o"
             "gpt-4o-mini"
             ;; open-weight
             "gpt-oss-120b"
             "gpt-oss-20b"
             ;; legacy
             "gpt-4-turbo"
             "gpt-4"
             "gpt-3.5-turbo"]
   "anthropic" [;; Current
                "claude-opus-4-6"
                "claude-sonnet-4-6"
                "claude-haiku-4-5"
                ;; Legacy
                "claude-sonnet-4-5"
                "claude-opus-4-5"
                "claude-opus-4-1"
                "claude-sonnet-4-0"
                "claude-opus-4-0"
                "claude-3-haiku-20240307"]
   "azure-openai" [;; GPT-5.3
                   "gpt-5.3-codex"
                   ;; GPT-5.2
                   "gpt-5.2"
                   "gpt-5.2-codex"
                   "gpt-5.2-chat"
                   ;; GPT-5.1
                   "gpt-5.1"
                   "gpt-5.1-chat"
                   "gpt-5.1-codex"
                   "gpt-5.1-codex-mini"
                   "gpt-5.1-codex-max"
                   ;; GPT-5
                   "gpt-5"
                   "gpt-5-pro"
                   "gpt-5-mini"
                   "gpt-5-nano"
                   "gpt-5-chat"
                   "gpt-5-codex"
                   ;; GPT-4.1
                   "gpt-4.1"
                   "gpt-4.1-mini"
                   "gpt-4.1-nano"
                   ;; o-series
                   "o3-pro"
                   "o3"
                   "o4-mini"
                   "o3-mini"
                   "o1"
                   "codex-mini"
                   ;; GPT-4o
                   "gpt-4o"
                   "gpt-4o-mini"
                   ;; legacy
                   "gpt-35-turbo"]
   "custom" []})
