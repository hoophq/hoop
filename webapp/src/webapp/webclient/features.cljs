(ns webapp.webclient.features
  "Which hoop features are active on the resource role selected in the Terminal.

   Single source of truth for the Features Indicator, so the name, description,
   icon and ordering of a feature live in exactly one place.

   Categories follow `Feature Specs | Major Features Visibility`:
   `:blocks-execution` features intercept the query and require an action before
   it runs; `:runs-alongside` features operate passively during the session."
  (:require
   ["lucide-react" :refer [EyeOff Package Shield Sparkles UserCheck]]
   [clojure.string :as cs]
   [webapp.config :as config]))

(def catalog
  "Ordered exactly as the hover popup and the modal list them: features that
   block execution first, then the ones that run alongside.

   Each entry carries an `:active?` predicate over the same context map, so
   every feature is decided the same way — see `active`."
  [{:id :access-request
    :name "Access Requests"
    :category :blocks-execution
    :description "Sends the query for reviewer approval before it executes."
    :icon [:> UserCheck {:size 16}]
    :tile-class "bg-[--indigo-11] text-white"
    ;; Same signal native client access uses to decide a connection requires
    ;; review — see connections/native_client_access/events.cljs.
    :active? (fn [{:keys [connection]}]
               (boolean (or (:jit_access_duration_sec connection)
                            (seq (:reviewers connection)))))}

   {:id :guardrails
    :name "Guardrails"
    :category :blocks-execution
    :description "Validates the query against active rules; blocks if a rule is violated."
    :icon [:> Shield {:size 16}]
    :tile-class "bg-[--blue-11] text-white"
    :active? (fn [{:keys [connection]}]
               (boolean (seq (:guardrail_rules connection))))}

   {:id :mandatory-metadata
    :name "Mandatory Metadata Fields"
    :category :blocks-execution
    :description "Requires the user to fill in required fields before the session starts."
    :icon [:> Package {:size 16}]
    :tile-class "bg-[--orange-11] text-white"
    :active? (fn [{:keys [connection]}]
               (boolean (seq (:mandatory_metadata_fields connection))))}

   {:id :ai-session-analyzer
    :name "AI Session Analyzer"
    :category :runs-alongside
    :description "Analyzes the session in real time and flags risk-based actions."
    :icon [:> Sparkles {:size 16}]
    :tile-class "bg-[--violet-11] text-white"
    ;; The analyzer rule is fetched per resource role into its own re-frame
    ;; subtree, so it reaches us through the context instead of the connection.
    :active? (fn [{:keys [analyzer-rule?]}]
               (boolean analyzer-rule?))}

   {:id :jira-templates
    :name "Jira Templates"
    :category :runs-alongside
    :description "Automatically creates a Jira ticket to log the data access event."
    :icon [:img {:src (str config/webapp-url "/icons/icon-jira.svg")
                 :alt ""
                 :class "w-4 h-4"}]
    :tile-class "bg-white border border-[--gray-4]"
    :active? (fn [{:keys [connection]}]
               (not (cs/blank? (:jira_issue_template_id connection))))}

   {:id :live-data-masking
    :name "Live Data Masking"
    :category :runs-alongside
    :description "Masks sensitive fields in query results in real time."
    :icon [:> EyeOff {:size 16}]
    :tile-class "bg-[--violet-11] text-white"
    :active? (fn [{:keys [connection]}]
               (boolean (:redact_enabled connection)))}])

(defn active
  "Ordered vector of the features active for `ctx`.

   `ctx` is `{:connection <connection map> :analyzer-rule? <boolean>}`."
  [ctx]
  (filterv #((:active? %) ctx) catalog))
