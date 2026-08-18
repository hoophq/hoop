(ns webapp.jira-templates.cascade-test
  "Field-config-driven cascade for CMDB dropdowns in the runtime prompt.

  Each Assets custom field carries its own AQL configuration in Jira: an
  object scope filter plus an optional issue scope filter that references
  sibling fields with ${customfield_x.label} placeholders. That configuration
  replaces the template's object type — it already defines every object the
  field accepts. Fields without a configuration (plain text fields) keep the
  template scope and never cascade."
  (:require
   [cljs.test :refer-macros [deftest testing is]]
   [webapp.jira-templates.cascade :as cascade]))

(def ^:private product
  {:jira_field "customfield_10091" :label "Produto" :jira_object_type "76"
   :value "ws:103" :selected-name "Git"
   :jira_values [{:id "ws:103" :name "Git"} {:id "ws:69" :name "Hoop"}]})

(def ^:private service
  {:jira_field "customfield_10092" :label "Serviço" :jira_object_type "77"})

(def ^:private text-row
  {:jira_field "customfield_10056" :label "second" :jira_object_type "77"})

;; mirrors /rest/servicedesk/cmdb/latest/fieldconfig responses
(def ^:private fieldconfigs
  (cascade/index-configs
   [{:jira_field "customfield_10091"
     :object_schema_id "2"
     :object_filter_query "objectType = \"Produto\""
     :issue_scope_filter_query ""}
    {:jira_field "customfield_10092"
     :object_schema_id "2"
     :object_filter_query ""
     :issue_scope_filter_query
     "objectType = \"Serviço\" AND Produto = ${customfield_10091.label}"}]))

(deftest static-object-scope-applies-without-any-selection
  (is (= {:aql "objectType = \"Produto\"" :object-schema-id "2"}
         (cascade/filter-for product [product service] fieldconfigs))))

(deftest issue-scope-resolves-placeholder-with-selected-name
  (is (= {:aql "objectType = \"Serviço\" AND Produto = \"Git\"" :object-schema-id "2"}
         (cascade/filter-for service [product service] fieldconfigs))))

(deftest unresolved-placeholder-is-pending-not-unfiltered
  (testing "a configured field must not fall back to the template scope"
    (let [unselected (dissoc product :value :selected-name)]
      (is (= :pending (cascade/filter-for service [unselected service] fieldconfigs))))))

(deftest unconfigured-fields-keep-the-template-scope
  (testing "plain text fields have no config entry"
    (is (nil? (cascade/filter-for text-row [product text-row] fieldconfigs)))
    (is (empty? (cascade/dependents-of product [product text-row] fieldconfigs)))))

(deftest configured-without-any-filter-keeps-the-template-scope
  (let [cfgs (cascade/index-configs
              [{:jira_field "customfield_10092" :object_schema_id "2"
                :object_filter_query "" :issue_scope_filter_query ""}])]
    (is (nil? (cascade/filter-for service [product service] cfgs)))))

(deftest object-and-issue-scopes-are-and-composed
  (let [cfgs (cascade/index-configs
              [{:jira_field "customfield_10092"
                :object_schema_id "2"
                :object_filter_query "objectType = \"Serviço\""
                :issue_scope_filter_query "Produto = ${customfield_10091.label}"}])]
    (is (= "(objectType = \"Serviço\") AND (Produto = \"Git\")"
           (:aql (cascade/filter-for service [product service] cfgs))))))

(deftest placeholder-suffixes
  (testing "bare placeholder behaves like .label"
    (let [cfgs (cascade/index-configs
                [{:jira_field "customfield_10092"
                  :issue_scope_filter_query "Produto = ${customfield_10091}"}])]
      (is (= "Produto = \"Git\""
             (:aql (cascade/filter-for service [product service] cfgs))))))
  (testing "other suffixes substitute the raw row value"
    (let [cfgs (cascade/index-configs
                [{:jira_field "customfield_10092"
                  :issue_scope_filter_query "Produto = ${customfield_10091.id}"}])]
      (is (= "Produto = \"ws:103\""
             (:aql (cascade/filter-for service [product service] cfgs)))))))

(deftest selected-name-fallbacks
  (testing "id looked up in loaded values when selection name wasn't captured"
    (let [prod (dissoc product :selected-name)]
      (is (= "objectType = \"Serviço\" AND Produto = \"Git\""
             (:aql (cascade/filter-for service [prod service] fieldconfigs))))))
  (testing "config-prefilled plain name is used as-is"
    (let [prod (-> product (dissoc :selected-name :jira_values) (assoc :value "Hoop"))]
      (is (= "objectType = \"Serviço\" AND Produto = \"Hoop\""
             (:aql (cascade/filter-for service [prod service] fieldconfigs)))))))

(deftest substituted-values-are-escaped
  (testing "quotes"
    (let [prod (assoc product :selected-name "Gi\"t")]
      (is (= "objectType = \"Serviço\" AND Produto = \"Gi\\\"t\""
             (:aql (cascade/filter-for service [prod service] fieldconfigs))))))
  (testing "a trailing backslash cannot swallow the closing quote"
    (let [prod (assoc product :selected-name "Git\\")]
      (is (= "objectType = \"Serviço\" AND Produto = \"Git\\\\\""
             (:aql (cascade/filter-for service [prod service] fieldconfigs)))))))

(deftest dependents-follow-placeholder-references
  (testing "only rows whose issue scope references the changed field react"
    (is (= ["customfield_10092"]
           (map :jira_field
                (cascade/dependents-of product [product service text-row] fieldconfigs)))))
  (testing "the dependent row itself has no dependents"
    (is (empty? (cascade/dependents-of service [product service text-row] fieldconfigs)))))

(deftest all-dependents-clears-chains-transitively
  (let [component {:jira_field "customfield_10093" :label "Componente"}
        cfgs (cascade/index-configs
              [{:jira_field "customfield_10092"
                :issue_scope_filter_query "Produto = ${customfield_10091.label}"}
               {:jira_field "customfield_10093"
                :issue_scope_filter_query "Serviço = ${customfield_10092.label}"}])]
    (testing "A -> B -> C: selecting A clears B and C"
      (is (= ["customfield_10092" "customfield_10093"]
             (map :jira_field
                  (cascade/all-dependents product [product service component text-row] cfgs)))))
    (testing "selecting B clears only C"
      (is (= ["customfield_10093"]
             (map :jira_field
                  (cascade/all-dependents service [product service component] cfgs)))))
    (testing "cyclic references terminate"
      (let [cyclic (cascade/index-configs
                    [{:jira_field "customfield_10092"
                      :issue_scope_filter_query "Produto = ${customfield_10091.label}"}
                     {:jira_field "customfield_10093"
                      :issue_scope_filter_query "Serviço = ${customfield_10092.label}"}
                     {:jira_field "customfield_10091"
                      :issue_scope_filter_query "Componente = ${customfield_10093.label}"}])]
        (is (= ["customfield_10092" "customfield_10093"]
               (map :jira_field
                    (cascade/all-dependents product [product service component] cyclic))))))))
