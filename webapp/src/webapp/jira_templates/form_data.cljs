(ns webapp.jira-templates.form-data
  "Form state of the runtime prompt, without UI: what the form starts with,
   what a CMDB pick changes and what Send posts. webapp_v2 tests its port
   against these functions (npm run fixtures:jira)."
  (:require [webapp.jira-templates.cascade :as cascade]))

(defn- get-value-id [jira_values value]
  (when-let [value-match (first (filter #(= (:name %) value) jira_values))]
    {"value" (:id value-match) "label" (:name value-match)}))

(defn init-form-data [cmdb-items]
  (reduce (fn [acc {:keys [jira_field jira_values value]}]
            (if-let [id (get-value-id jira_values value)]
              (assoc-in acc [:jira_fields jira_field] id)
              acc))
          {}
          cmdb-items))

(defn- process-datetime-field [prompts field-key field-value]
  (let [prompt (first (filter #(= (:jira_field %) field-key) prompts))]
    (if (and (= (:field_type prompt) "datetime-local")
             (not (empty? field-value)))
      (-> (js/Date. field-value)
          .toISOString)
      field-value)))

(defn select-cmdb
  "Form data after a CMDB pick. Rows the Assets schema filters by this
   selection are cleared and refetched (db side handled by
   update-cmdb-value); drop their stale picks from the local form state too."
  [form-data cmdb-item all-items fieldconfigs value]
  (reduce (fn [acc dep] (update acc :jira_fields dissoc (:jira_field dep)))
          (assoc-in form-data [:jira_fields (:jira_field cmdb-item)] value)
          (cascade/all-dependents cmdb-item all-items fieldconfigs)))

(defn submit-data
  "What Send hands to on-submit: a CMDB pick becomes its object id and a
   datetime-local value becomes an ISO instant."
  [prompts form-data]
  (update form-data :jira_fields
          (fn [fields]
            (reduce-kv
             (fn [m k v]
               (assoc m k
                      (cond
                        (map? v) (get v "value")
                        :else (process-datetime-field prompts k v))))
             {}
             fields))))
