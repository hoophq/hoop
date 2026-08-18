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

(defn- quote-aql
  "AQL string literal: backslashes escaped before quotes so a trailing
   backslash cannot swallow the closing quote."
  [s]
  (str "\"" (-> s
                (cs/replace "\\" "\\\\")
                (cs/replace "\"" "\\\"")) "\""))

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

(defn all-dependents
  "Transitive closure of dependents-of, breadth-first: a cleared row clears
   its own dependents in turn (A -> B -> C), so a chain never keeps a pick
   that was filtered by a now-cleared upstream. The seen set makes cyclic
   schema references terminate."
  [changed-item items relations]
  (loop [frontier [changed-item]
         seen #{(:jira_field changed-item)}
         acc []]
    (if-let [item (first frontier)]
      (let [deps (remove #(contains? seen (:jira_field %))
                         (dependents-of item items relations))]
        (recur (into (vec (rest frontier)) deps)
               (into seen (map :jira_field deps))
               (into acc deps)))
      acc)))

(defn aql-for
  "AQL filter for item derived from the Assets schema: one
   \"Attribute\" = \"Selected name\" clause per reference attribute of the
   item's object type whose target type has a selected sibling row. When
   several rows share the referenced type, the first row with a selection
   wins. Returns nil when nothing applies, so callers fall back to the
   unfiltered fetch."
  [item items relations]
  (let [clauses (keep (fn [{:keys [attribute_name reference_object_type_id]}]
                        (let [nm (->> items
                                      (filter #(and (not= (:jira_field %) (:jira_field item))
                                                    (= (:jira_object_type %) reference_object_type_id)))
                                      (keep selected-name)
                                      (remove cs/blank?)
                                      first)]
                          (when nm
                            (str (quote-aql attribute_name) " = " (quote-aql nm)))))
                      (filter #(= (:object_type_id %) (:jira_object_type item)) relations))]
    (when (seq clauses)
      (cs/join " AND " clauses))))
