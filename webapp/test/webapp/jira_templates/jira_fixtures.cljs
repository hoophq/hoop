(ns webapp.jira-templates.jira-fixtures
  "Writes the Jira prompt gate vectors as JSON fixtures for the webapp_v2
  tests. Each case holds its input and the output of the ClojureScript code,
  which must equal the case's expectation. Run: npm run fixtures:jira"
  (:require
   ["fs" :as fs]
   ["path" :as path]
   [webapp.jira-templates.jira-vectors :as v]))

(def ^:private source "webapp/test/webapp/jira_templates/jira_vectors.cljs")
(def ^:private generator "npm --prefix webapp run fixtures:jira")

(defn- filter-json [output]
  (cond
    (nil? output) nil
    (= :pending output) "pending"
    :else {"aql" (:aql output) "object_schema_id" (:object-schema-id output)}))

(def ^:private fixtures
  [{:file "jira_submit.json"
    :function "webapp.jira-templates.form-data/submit-data"
    :cases v/submit-cases
    :run v/run-submit
    :json (fn [c output] {"name" (:name c)
                          "prompts" (or (:prompts c) [])
                          "cmdb_items" (or (:cmdb-items c) [])
                          "fieldconfigs" (or (:fieldconfigs c) [])
                          "events" (mapv (fn [[kind field value]]
                                           {"kind" kind "jira_field" field "value" value})
                                         (:events c))
                          "expected" output})}
   {:file "jira_cascade_filter.json"
    :function "webapp.jira-templates.cascade/filter-for"
    :cases v/filter-cases
    :run v/run-filter
    :json (fn [c output] {"name" (:name c)
                          "item" (:item c)
                          "items" (:items c)
                          "fieldconfigs" (:fieldconfigs c)
                          "expected" (filter-json output)})}
   {:file "jira_cascade_dependents.json"
    :function "webapp.jira-templates.cascade/all-dependents"
    :cases v/dependents-cases
    :run v/run-dependents
    :json (fn [c output] {"name" (:name c)
                          "changed" (:changed c)
                          "items" (:items c)
                          "fieldconfigs" (:fieldconfigs c)
                          "expected" output})}])

(defn- fixture-cases
  "The JSON cases of one fixture, or throws on a case whose output differs
  from its expectation: a fixture never records a behavior nobody checked."
  [{:keys [file cases run json]}]
  (mapv (fn [c]
          (let [output (run c)]
            (when-not (= (:expected c) output)
              (throw (ex-info (str file ": case \"" (:name c) "\" fails its expectation")
                              {:expected (:expected c) :output output})))
            (json c output)))
        cases))

(defn main [& [out-dir]]
  (when-not out-dir
    (throw (ex-info "usage: node target/jira-fixtures.js <out-dir>" {})))
  (when-not (= v/timezone (.. js/Intl DateTimeFormat resolvedOptions -timeZone))
    (throw (ex-info (str "run with TZ=" v/timezone) {})))
  (fs/mkdirSync out-dir #js {:recursive true})
  (doseq [{:keys [file function] :as fixture} fixtures]
    (let [doc {"source" source
               "generator" generator
               "function" function
               "timezone" v/timezone
               "cases" (fixture-cases fixture)}
          target (path/join out-dir file)]
      (fs/writeFileSync target (str (js/JSON.stringify (clj->js doc) nil 2) "\n"))
      (println "wrote" target))))
