(ns webapp.webclient.features
  "Which hoop features are active on the resource role selected in the Terminal.

   Single source of truth for the Features Indicator, so the name, description,
   icon and ordering of a feature live in exactly one place.

   Every predicate reads `:effective_features`, which the gateway resolves the
   same way it enforces — covering rules attached directly to the connection and
   rules attached through an attribute, the mechanism protection profiles use.
   The connection's own columns are NOT a substitute: `guardrail_rules` and
   `redact_enabled` only ever reflect direct associations, so reading them makes
   the indicator silent for a profile-governed connection.

   When `:effective_features` is absent the gateway could not resolve it (it logs
   the failure and sends null rather than a set of falses). Everything then reads
   as inactive and the indicator hides itself — the same as having no feature
   configured. That is a deliberate choice not to invent a third UI state for a
   case the server already reports.

   Categories follow `Feature Specs | Major Features Visibility`:
   `:blocks-execution` features intercept the query and require an action before
   it runs; `:runs-alongside` features operate passively during the session."
  (:require
   ["lucide-react" :refer [EyeOff Package Shield Sparkles UserCheck]]
   [webapp.config :as config]))

(defn- feature-on? [connection & path]
  (boolean (get-in connection (into [:effective_features] path))))

(def catalog
  "Ordered exactly as the hover popup and the modal list them: features that
   block execution first, then the ones that run alongside."
  [{:id :access-request
    :name "Access Requests"
    :category :blocks-execution
    :description "Sends the query for reviewer approval before it executes."
    :icon [:> UserCheck {:size 16}]
    :tile-class "bg-[--indigo-11] text-white"
    ;; The Terminal executes, so the gateway resolves the "command" rule — "jit"
    ;; governs the native client and does not apply here. The legacy review
    ;; plugin still applies on top of it.
    :active? (fn [connection]
               (or (feature-on? connection :access_request :command)
                   (feature-on? connection :access_request :legacy_reviewers)))}

   {:id :guardrails
    :name "Guardrails"
    :category :blocks-execution
    :description "Validates the query against active rules; blocks if a rule is violated."
    :icon [:> Shield {:size 16}]
    :tile-class "bg-[--blue-11] text-white"
    :active? (fn [connection] (feature-on? connection :guardrails))}

   {:id :mandatory-metadata
    :name "Mandatory Metadata Fields"
    :category :blocks-execution
    :description "Requires the user to fill in required fields before the session starts."
    :icon [:> Package {:size 16}]
    :tile-class "bg-[--orange-11] text-white"
    :active? (fn [connection] (feature-on? connection :mandatory_metadata))}

   {:id :ai-session-analyzer
    :name "AI Session Analyzer"
    :category :runs-alongside
    :description "Analyzes the session in real time and flags risk-based actions."
    :icon [:> Sparkles {:size 16}]
    :tile-class "bg-[--violet-11] text-white"
    :active? (fn [connection] (feature-on? connection :ai_session_analyzer))}

   {:id :jira-templates
    :name "Jira Templates"
    :category :runs-alongside
    :description "Automatically creates a Jira ticket to log the data access event."
    :icon [:img {:src (str config/webapp-url "/icons/icon-jira.svg")
                 :alt ""
                 :class "w-4 h-4"}]
    ;; Light blue rather than the solid fill the other tiles use: this is the
    ;; only icon that is a brand logo with its own colours, and a saturated
    ;; background would swallow it. icon-jira.svg carries its own fills — the
    ;; current-color variant exists but cannot be tinted through an <img>.
    :tile-class "bg-[--blue-3] border border-[--blue-6]"
    :active? (fn [connection] (feature-on? connection :jira_templates))}

   {:id :live-data-masking
    :name "Live Data Masking"
    :category :runs-alongside
    :description "Masks sensitive fields in query results in real time."
    :icon [:> EyeOff {:size 16}]
    :tile-class "bg-[--violet-11] text-white"
    ;; The gateway already accounts for the masking provider being active, so a
    ;; true here means masking will really run, not just that rules exist.
    :active? (fn [connection] (feature-on? connection :data_masking))}])

(defn active
  "Ordered vector of the features active on `connection`."
  [connection]
  (filterv #((:active? %) connection) catalog))
