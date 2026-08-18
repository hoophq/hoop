(ns webapp.jira-templates.cascade
  "Schema-driven cascade for CMDB dropdowns in the runtime prompt.

   The Jira Assets schema defines the edges: relations are the
   object-reference attributes fetched from the Assets API, each
   {:object_type_id :attribute_name :reference_object_type_id}. A dropdown
   whose object type carries a reference attribute is filtered by an AQL
   clause per attribute whose target type has a selected sibling row."
  (:require [clojure.string :as cs]))

(defn- selected-name
  "Selected object name for a row: prefer the name captured at selection
   time, then a lookup of the selected id in the loaded values, then the
   raw :value (a plain name when prefilled by the template config)."
  [{:keys [value jira_values] :as item}]
  (or (:selected-name item)
      (:name (first (filter #(= (:id %) value) jira_values)))
      (when-not (cs/blank? value) value)))

(defn- quote-aql [s]
  (str "\"" (cs/replace s "\"" "\\\"") "\""))

(defn dependents-of
  "Rows whose object type has a reference attribute pointing at the
   changed row's object type."
  [changed-item items relations]
  (let [changed-type (:jira_object_type changed-item)
        dependent-types (into #{}
                              (comp (filter #(= (:reference_object_type_id %) changed-type))
                                    (map :object_type_id))
                              relations)]
    (filter #(and (not= (:jira_field %) (:jira_field changed-item))
                  (contains? dependent-types (:jira_object_type %)))
            items)))

(defn aql-for
  "AQL filter for item derived from the Assets schema: one
   \"Attribute\" = \"Selected name\" clause per reference attribute of the
   item's object type whose target type has a selected sibling row. Returns
   nil when nothing applies, so callers fall back to the unfiltered fetch."
  [item items relations]
  (let [clauses (keep (fn [{:keys [attribute_name reference_object_type_id]}]
                        (let [upstream (first (filter #(and (not= (:jira_field %) (:jira_field item))
                                                            (= (:jira_object_type %) reference_object_type_id))
                                                      items))
                              nm (some-> upstream selected-name)]
                          (when-not (cs/blank? nm)
                            (str (quote-aql attribute_name) " = " (quote-aql nm)))))
                      (filter #(= (:object_type_id %) (:jira_object_type item)) relations))]
    (when (seq clauses)
      (cs/join " AND " clauses))))
