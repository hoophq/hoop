(ns webapp.features.activation-journey.templates
  "Recommended feature templates for the product activation journey.

  Features always appear in this order: Guardrails, Live Data Masking,
  AI Session Analyzer. Cards, banners, and the See Features modal all
  derive their copy and deep-link targets from this catalog.

  Sources of truth (Feature Specs | Product Activation Journey, EVL-69):
  - Guardrails:          default-guardrails.json          -> guardrail-templates ns
  - Live Data Masking:   live-data-masking-templates.json -> webapp_v2 .../DataMasking/templates.js
                         (this ns keeps a lightweight metadata mirror; the full
                         rule bodies live on the React side, which owns the page)
  - AI Session Analyzer: ai-session-analyzer-templates.json"
  (:require
   [webapp.features.activation-journey.guardrail-templates :as guardrail-templates]))

;; Deep-link contract: a feature page opened with ?template=<id> pre-applies
;; the matching template. For guardrails and AI analyzer templates the id is
;; the payload :name; masking templates carry an explicit :id kept in sync
;; with MASKING_TEMPLATES in webapp_v2/src/pages/Features/DataMasking/templates.js.

;; TODO(EVL-69): replace with the content of live-data-masking-templates.json
;; (Linear attachment). :name must match the rule name in the React
;; MASKING_TEMPLATES entry with the same :id so configured-detection works.
(def masking-templates
  [{:id "mask-sensitive-field-type"
    :title "Mask 1 sensitive field type"
    :card-description "Matches PII in query results and redacts it before the response reaches the client."
    :name "Mask sensitive fields"}])

;; TODO(EVL-69): replace with the content of ai-session-analyzer-templates.json
;; (Linear attachment). Each entry is the POST /ai/session-analyzer/rules body
;; plus the UI-only :title and :card-description keys.
(def ai-analyzer-templates
  [{:title "Sessions above risk levels"
    :card-description "Reviews every session for suspicious activity. Stops operations that exceed your risk limit."
    :name "block-high-risk-sessions"
    :description "Reviews every session for suspicious activity and stops operations that exceed your risk limit."
    :connection_names []
    :risk_evaluation {:low_risk {:action "allow_execution"}
                      :medium_risk {:action "allow_execution"}
                      :high_risk {:action "block_execution"}}
    :custom_prompt nil}])

(def features
  "Ordered catalog of the three activation-journey features. The order is a
  product requirement (Guardrails -> Live Data Masking -> AI Session Analyzer)
  and must stay consistent across cards, banners, and modal states."
  [{:feature :guardrails
    :label "Guardrails"
    :generic-description "Create custom rules to guide and protect usage within your resources."
    :banner-cta "Apply Guardrail"
    :route-create :create-guardrail
    :route-list :guardrails
    :connections-param :ids}
   {:feature :masking
    :label "Live Data Masking"
    :generic-description "Matches PII in query results and redacts it before the response reaches the client."
    :banner-cta "Activate Live Data Masking"
    :route-create :create-ai-data-masking
    :route-list :ai-data-masking
    :connections-param :ids}
   {:feature :ai-analyzer
    :label "AI Session Analyzer"
    :generic-description "Reviews every session for suspicious activity. Stops operations that exceed your risk limit."
    :banner-cta "Activate AI Session Analyzer"
    :route-create :create-ai-session-analyzer-rule
    :route-list :ai-session-analyzer
    :connections-param :names}])

(defn templates-for
  "Ordered recommended templates for a feature. Guardrails templates are
  curated per connection subtype; an unknown subtype yields [] and the card
  degrades to the generic feature copy without a template deep link."
  [feature subtype]
  (case feature
    :guardrails (guardrail-templates/for-subtype subtype)
    :masking masking-templates
    :ai-analyzer ai-analyzer-templates
    []))

(defn template-link-id
  "The ?template= value for a template entry."
  [template]
  (or (:id template) (:name template)))

(defn find-guardrail-template [template-id]
  (guardrail-templates/find-by-name template-id))

(defn find-ai-analyzer-template [template-id]
  (some #(when (= (:name %) template-id) %) ai-analyzer-templates))

(defn guardrail-payload
  "Strips UI-only keys, leaving the exact POST /guardrails body."
  [template]
  (dissoc template :title :card-description))

(defn ai-analyzer-payload
  "Strips UI-only keys, leaving the exact POST /ai/session-analyzer/rules body."
  [template]
  (dissoc template :title :card-description))
