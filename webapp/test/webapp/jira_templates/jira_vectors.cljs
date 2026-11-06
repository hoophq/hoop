(ns webapp.jira-templates.jira-vectors
  "Inputs and expectations of the Jira prompt gate. jira-fixtures runs each
  case through the ClojureScript code and writes it as a JSON fixture of
  webapp_v2 (npm run fixtures:jira)."
  (:require
   [webapp.jira-templates.cascade :as cascade]
   [webapp.jira-templates.form-data :as form-data]))

;; The datetime-local cases convert a wall-clock time of this zone. The
;; generator and the webapp_v2 test both run in it.
(def timezone "America/Sao_Paulo")

;; ─── Templates ─────────────────────────────────────────────────────────────

(defn- prompt [jira-field field-type & {:keys [required options]}]
  (cond-> {:label (str "Label of " jira-field)
           :jira_field jira-field
           :field_type field-type
           :required (boolean required)
           :description ""}
    options (assoc :field_options options)))

(def ^:private servers
  [{:id "ari:server:1" :name "db-prod-01"}
   {:id "ari:server:2" :name "db-prod-02"}])

(def ^:private products
  [{:id "ari:product:1" :name "Payments"}
   {:id "ari:product:2" :name "Ledger"}])

(def ^:private components
  [{:id "ari:component:1" :name "API"}])

(defn- cmdb [jira-field object-type values & {:keys [value required]}]
  {:label (str "Label of " jira-field)
   :jira_field jira-field
   :jira_object_type object-type
   :jira_object_schema_id "7"
   :required (boolean required)
   :description ""
   :value value
   :jira_values values})

;; Product -> Component -> Version: each issue scope names the row above.
(def ^:private chain-configs
  [{:jira_field "customfield_20002"
    :object_schema_id "7"
    :object_filter_query "objectType = \"Component\""
    :issue_scope_filter_query "Product = ${customfield_20001.label}"}
   {:jira_field "customfield_20003"
    :object_schema_id "7"
    :object_filter_query ""
    :issue_scope_filter_query "Component = ${customfield_20002}"}])

(def ^:private chain-items
  [(cmdb "customfield_20001" "11" products :value "Payments")
   (cmdb "customfield_20002" "12" components :value "API")
   (cmdb "customfield_20003" "13" [{:id "ari:version:1" :name "v1"}] :value "v1")])

;; ─── form-data: init-form-data, select-cmdb, submit-data ───────────────────

(def submit-cases
  [{:name "text prompt"
    :prompts [(prompt "customfield_10001" "text" :required true)]
    :events [["prompt" "customfield_10001" "rollback after the deploy"]]
    :expected {:jira_fields {"customfield_10001" "rollback after the deploy"}}}

   {:name "prompt with no field type is a text prompt"
    :prompts [(prompt "customfield_10001" "")]
    :events [["prompt" "customfield_10001" "free text"]]
    :expected {:jira_fields {"customfield_10001" "free text"}}}

   {:name "text prompt cleared after typing is sent empty"
    :prompts [(prompt "customfield_10001" "text")]
    :events [["prompt" "customfield_10001" "draft"]
             ["prompt" "customfield_10001" ""]]
    :expected {:jira_fields {"customfield_10001" ""}}}

   {:name "select prompt"
    :prompts [(prompt "customfield_10002" "select" :required true :options ["low" "medium" "high"])]
    :events [["prompt" "customfield_10002" "high"]]
    :expected {:jira_fields {"customfield_10002" "high"}}}

   {:name "datetime-local prompt is sent as an ISO instant"
    :prompts [(prompt "customfield_10003" "datetime-local" :required true)]
    :events [["prompt" "customfield_10003" "2026-10-28T14:30"]]
    :expected {:jira_fields {"customfield_10003" "2026-10-28T17:30:00.000Z"}}}

   {:name "datetime-local prompt cleared after typing is sent empty"
    :prompts [(prompt "customfield_10003" "datetime-local")]
    :events [["prompt" "customfield_10003" "2026-10-28T14:30"]
             ["prompt" "customfield_10003" ""]]
    :expected {:jira_fields {"customfield_10003" ""}}}

   {:name "unanswered optional prompts send an empty map"
    :prompts [(prompt "customfield_10001" "text")
              (prompt "customfield_10002" "select" :options ["a"])]
    :events []
    :expected {:jira_fields {}}}

   {:name "every prompt type together"
    :prompts [(prompt "customfield_10001" "text" :required true)
              (prompt "customfield_10002" "select" :options ["low" "high"])
              (prompt "customfield_10003" "datetime-local")]
    :events [["prompt" "customfield_10002" "low"]
             ["prompt" "customfield_10001" "hotfix"]
             ["prompt" "customfield_10003" "2026-01-05T09:00"]]
    :expected {:jira_fields {"customfield_10001" "hotfix"
                             "customfield_10002" "low"
                             "customfield_10003" "2026-01-05T12:00:00.000Z"}}}

   {:name "CMDB preset found in the values is sent as its object id"
    :cmdb-items [(cmdb "customfield_30001" "21" servers :value "db-prod-02" :required true)]
    :events []
    :expected {:jira_fields {"customfield_30001" "ari:server:2"}}}

   {:name "CMDB preset missing from the values is not sent"
    :cmdb-items [(cmdb "customfield_30001" "21" servers :value "db-prod-99" :required true)]
    :events []
    :expected {:jira_fields {}}}

   {:name "CMDB row without a preset and no pick is not sent"
    :cmdb-items [(cmdb "customfield_30001" "21" servers)]
    :events []
    :expected {:jira_fields {}}}

   {:name "CMDB pick replaces the preset"
    :cmdb-items [(cmdb "customfield_30001" "21" servers :value "db-prod-01")]
    :events [["cmdb" "customfield_30001" "ari:server:2"]]
    :expected {:jira_fields {"customfield_30001" "ari:server:2"}}}

   {:name "CMDB pick on a row whose preset is missing"
    :cmdb-items [(cmdb "customfield_30001" "21" servers :value "db-prod-99" :required true)]
    :events [["cmdb" "customfield_30001" "ari:server:1"]]
    :expected {:jira_fields {"customfield_30001" "ari:server:1"}}}

   {:name "CMDB picks and prompts in one body"
    :prompts [(prompt "customfield_10001" "text" :required true)]
    :cmdb-items [(cmdb "customfield_30001" "21" servers :value "db-prod-01")]
    :events [["prompt" "customfield_10001" "reason"]]
    :expected {:jira_fields {"customfield_10001" "reason"
                             "customfield_30001" "ari:server:1"}}}

   {:name "a pick drops the picks of the rows that depend on it, transitively"
    :cmdb-items chain-items
    :fieldconfigs chain-configs
    :events [["cmdb" "customfield_20001" "ari:product:2"]]
    :expected {:jira_fields {"customfield_20001" "ari:product:2"}}}

   {:name "a pick on the middle row keeps the row above"
    :cmdb-items chain-items
    :fieldconfigs chain-configs
    :events [["cmdb" "customfield_20002" "ari:component:1"]]
    :expected {:jira_fields {"customfield_20001" "ari:product:1"
                             "customfield_20002" "ari:component:1"}}}

   {:name "a pick on a row nothing depends on drops nothing"
    :cmdb-items chain-items
    :fieldconfigs chain-configs
    :events [["cmdb" "customfield_20003" "ari:version:1"]]
    :expected {:jira_fields {"customfield_20001" "ari:product:1"
                             "customfield_20002" "ari:component:1"
                             "customfield_20003" "ari:version:1"}}}

   {:name "without field configs a pick drops nothing"
    :cmdb-items chain-items
    :events [["cmdb" "customfield_20001" "ari:product:2"]]
    :expected {:jira_fields {"customfield_20001" "ari:product:2"
                             "customfield_20002" "ari:component:1"
                             "customfield_20003" "ari:version:1"}}}])

(defn run-submit
  "The form data the prompt form builds from these events, as Send posts it.
   A prompt event is the on-change of its input; a CMDB event is the
   searchbox's on-select with the picked object id."
  [{:keys [prompts cmdb-items fieldconfigs events]}]
  (let [configs (cascade/index-configs fieldconfigs)
        data (reduce (fn [data [kind jira-field value]]
                       (case kind
                         "prompt" (assoc-in data [:jira_fields jira-field] value)
                         "cmdb" (form-data/select-cmdb
                                 data
                                 (first (filter #(= (:jira_field %) jira-field) cmdb-items))
                                 cmdb-items configs value)))
                     (form-data/init-form-data cmdb-items)
                     events)]
    (form-data/submit-data prompts data)))

;; ─── cascade/filter-for ────────────────────────────────────────────────────

(defn- row [jira-field & {:keys [value selected-name values]}]
  (cond-> {:jira_field jira-field :jira_values (or values [])}
    value (assoc :value value)
    selected-name (assoc :selected-name selected-name)))

(def filter-cases
  [{:name "a field without a configuration keeps the object type scope"
    :item "customfield_1"
    :items [(row "customfield_1")]
    :fieldconfigs []
    :expected nil}

   {:name "a configuration with no filter keeps the object type scope"
    :item "customfield_1"
    :items [(row "customfield_1")]
    :fieldconfigs [{:jira_field "customfield_1" :object_schema_id "7"
                    :object_filter_query "" :issue_scope_filter_query ""}]
    :expected nil}

   {:name "the object filter alone"
    :item "customfield_1"
    :items [(row "customfield_1")]
    :fieldconfigs [{:jira_field "customfield_1" :object_schema_id "7"
                    :object_filter_query "objectType = \"Server\"" :issue_scope_filter_query ""}]
    :expected {:aql "objectType = \"Server\"" :object-schema-id "7"}}

   {:name "an issue scope waits while the row it names has no selection"
    :item "customfield_2"
    :items [(row "customfield_1") (row "customfield_2")]
    :fieldconfigs [{:jira_field "customfield_2" :object_schema_id "7"
                    :object_filter_query "objectType = \"Component\""
                    :issue_scope_filter_query "Product = ${customfield_1.label}"}]
    :expected :pending}

   {:name "an issue scope names a row the template does not have"
    :item "customfield_2"
    :items [(row "customfield_2")]
    :fieldconfigs [{:jira_field "customfield_2" :object_schema_id "7"
                    :object_filter_query ""
                    :issue_scope_filter_query "Product = ${customfield_9}"}]
    :expected :pending}

   {:name "both filters AND-composed, the label from the selected name"
    :item "customfield_2"
    :items [(row "customfield_1" :value "ari:product:2" :selected-name "Ledger")
            (row "customfield_2")]
    :fieldconfigs [{:jira_field "customfield_2" :object_schema_id "8"
                    :object_filter_query "objectType = \"Component\""
                    :issue_scope_filter_query "Product = ${customfield_1.label}"}]
    :expected {:aql "(objectType = \"Component\") AND (Product = \"Ledger\")" :object-schema-id "8"}}

   {:name "the label from the loaded values when no name was kept"
    :item "customfield_2"
    :items [(row "customfield_1" :value "ari:product:1" :values products)
            (row "customfield_2")]
    :fieldconfigs [{:jira_field "customfield_2" :object_schema_id "7"
                    :object_filter_query ""
                    :issue_scope_filter_query "Product = ${customfield_1}"}]
    :expected {:aql "Product = \"Payments\"" :object-schema-id "7"}}

   {:name "the label is the raw preset name before any pick"
    :item "customfield_2"
    :items [(row "customfield_1" :value "Payments")
            (row "customfield_2")]
    :fieldconfigs [{:jira_field "customfield_2" :object_schema_id "7"
                    :object_filter_query ""
                    :issue_scope_filter_query "Product = ${customfield_1.label}"}]
    :expected {:aql "Product = \"Payments\"" :object-schema-id "7"}}

   {:name "another suffix substitutes the raw value"
    :item "customfield_2"
    :items [(row "customfield_1" :value "ari:product:2" :selected-name "Ledger")
            (row "customfield_2")]
    :fieldconfigs [{:jira_field "customfield_2" :object_schema_id "7"
                    :object_filter_query ""
                    :issue_scope_filter_query "Product.id = ${customfield_1.id}"}]
    :expected {:aql "Product.id = \"ari:product:2\"" :object-schema-id "7"}}

   {:name "quotes and backslashes in a name are escaped"
    :item "customfield_2"
    :items [(row "customfield_1" :value "x" :selected-name "a\"b\\")
            (row "customfield_2")]
    :fieldconfigs [{:jira_field "customfield_2" :object_schema_id "7"
                    :object_filter_query ""
                    :issue_scope_filter_query "Name = ${customfield_1.label}"}]
    :expected {:aql "Name = \"a\\\"b\\\\\"" :object-schema-id "7"}}

   {:name "one unresolved placeholder of two waits"
    :item "customfield_3"
    :items [(row "customfield_1" :value "Payments")
            (row "customfield_2")
            (row "customfield_3")]
    :fieldconfigs [{:jira_field "customfield_3" :object_schema_id "7"
                    :object_filter_query ""
                    :issue_scope_filter_query "A = ${customfield_1} AND B = ${customfield_2}"}]
    :expected :pending}])

(defn run-filter [{:keys [item items fieldconfigs]}]
  (cascade/filter-for (first (filter #(= (:jira_field %) item) items))
                      items
                      (cascade/index-configs fieldconfigs)))

;; ─── cascade/all-dependents ────────────────────────────────────────────────

(defn- scope [jira-field issue-scope]
  {:jira_field jira-field :object_schema_id "7"
   :object_filter_query "" :issue_scope_filter_query issue-scope})

(def dependents-cases
  [{:name "a chain clears breadth-first"
    :changed "customfield_1"
    :items (mapv row ["customfield_1" "customfield_2" "customfield_3" "customfield_4"])
    :fieldconfigs [(scope "customfield_2" "P = ${customfield_1}")
                   (scope "customfield_3" "C = ${customfield_2.label}")
                   (scope "customfield_4" "P = ${customfield_1}")]
    :expected ["customfield_2" "customfield_4" "customfield_3"]}

   {:name "a cycle ends"
    :changed "customfield_1"
    :items (mapv row ["customfield_1" "customfield_2"])
    :fieldconfigs [(scope "customfield_1" "X = ${customfield_2}")
                   (scope "customfield_2" "Y = ${customfield_1}")]
    :expected ["customfield_2"]}

   {:name "a row naming itself is not its own dependent"
    :changed "customfield_1"
    :items (mapv row ["customfield_1"])
    :fieldconfigs [(scope "customfield_1" "X = ${customfield_1}")]
    :expected []}

   {:name "no configuration, no dependents"
    :changed "customfield_1"
    :items (mapv row ["customfield_1" "customfield_2"])
    :fieldconfigs []
    :expected []}])

(defn run-dependents [{:keys [changed items fieldconfigs]}]
  (mapv :jira_field
        (cascade/all-dependents (first (filter #(= (:jira_field %) changed) items))
                                items
                                (cascade/index-configs fieldconfigs))))
