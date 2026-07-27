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
  - AI Session Analyzer: ai-session-analyzer-templates.json -> ai-analyzer-templates ns"
  (:require
   [webapp.features.activation-journey.ai-analyzer-templates :as ai-analyzer-templates]
   [webapp.features.activation-journey.guardrail-templates :as guardrail-templates]))

;; Deep-link contract: a feature page opened with ?template=<id> pre-applies
;; the matching template. The id is the template's :name for every feature;
;; masking names are kept in sync with MASKING_TEMPLATES in
;; webapp_v2/src/pages/Features/DataMasking/templates.js.

;; Metadata mirror of live-data-masking-templates.json, ordered by
;; recommendation (developer access -> support team -> contractor). The full
;; rule bodies live in the React module above; :name is what configured-rule
;; detection matches against.
(def masking-templates
  [{:title "Mask PII for developer access"
    :card-description "Masks names, emails, phone numbers, and addresses in production query results."
    :name "prod-mask-pii-developer-access"}
   {:title "Mask sensitive customer data"
    :card-description "Masks PII, government IDs, and financial data in support-team account lookups."
    :name "support-mask-sensitive-customer-data"}
   {:title "Mask all sensitive data"
    :card-description "Maximum masking coverage for external contractors: PII, government IDs, financial and medical data."
    :name "contractor-mask-all-sensitive-data"}])

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
  "Ordered recommended templates for a feature. Guardrails and AI analyzer
  templates are curated per connection subtype; an unknown subtype yields []
  and the card degrades to the generic feature copy without a template deep
  link. Masking templates are per access scenario, not per subtype."
  [feature subtype]
  (case feature
    :guardrails (guardrail-templates/for-subtype subtype)
    :masking masking-templates
    :ai-analyzer (ai-analyzer-templates/for-subtype subtype)
    []))

(defn template-link-id
  "The ?template= value for a template entry."
  [template]
  (or (:id template) (:name template)))

(defn find-guardrail-template [template-id]
  (guardrail-templates/find-by-name template-id))

(defn find-ai-analyzer-template [template-id]
  (ai-analyzer-templates/find-by-name template-id))

(defn guardrail-payload
  "Strips UI-only keys, leaving the exact POST /guardrails body."
  [template]
  (dissoc template :title :card-description))

(defn ai-analyzer-payload
  "Strips UI-only keys, leaving the exact POST /ai/session-analyzer/rules body."
  [template]
  (dissoc template :title :card-description))
