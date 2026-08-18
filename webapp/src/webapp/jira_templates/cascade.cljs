(ns webapp.jira-templates.cascade
  "Field-config-driven cascade for CMDB dropdowns in the runtime prompt.

   Each Assets custom field carries its own AQL configuration in Jira (the
   same one the JSM portal applies): an object scope filter plus an optional
   issue scope filter that may reference sibling fields with
   ${customfield_x.label} placeholders — Jira's native dependent-field
   mechanism. fieldconfigs is a map of jira_field ->
   {:object-filter \"...\" :issue-scope \"...\"}; fields absent from the map
   (plain text fields, unconfigured Assets fields) never filter and never
   cascade."
  (:require [clojure.string :as cs]))

(def ^:private placeholder-re #"\$\{(customfield_\d+)(?:\.([A-Za-z]+))?\}")

(defn index-configs
  "API items [{:jira_field :object_schema_id :object_filter_query
   :issue_scope_filter_query}] -> {jira_field {:object-schema-id ...
   :object-filter ... :issue-scope ...}}."
  [items]
  (into {}
        (map (juxt :jira_field
                   #(hash-map :object-schema-id (:object_schema_id %)
                              :object-filter (:object_filter_query %)
                              :issue-scope (:issue_scope_filter_query %))))
        items))

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

(defn- placeholder-value
  "Quoted substitution for a ${customfield_x[.suffix]} placeholder: the
   sibling row's selected object name (label/bare) or raw value (any other
   suffix, e.g. .id). nil while the referenced row has no selection."
  [items field suffix]
  (when-let [row (first (filter #(= (:jira_field %) field) items))]
    (let [raw (if (contains? #{nil "label"} suffix)
                (selected-name row)
                (:value row))]
      (when-not (cs/blank? raw)
        (quote-aql raw)))))

(defn- substitute-placeholders
  "Resolve every placeholder in aql against the sibling rows' selections.
   Returns nil when any placeholder is unresolved: a partially substituted
   filter would silently show wrong options."
  [aql items]
  (let [unresolved? (atom false)
        resolved (cs/replace aql placeholder-re
                             (fn [[_ field suffix]]
                               (or (placeholder-value items field suffix)
                                   (do (reset! unresolved? true) ""))))]
    (when-not @unresolved? resolved)))

(defn filter-for
  "How to scope item's dropdown, from its Jira field configuration:

     nil       - the field has no Assets configuration; the caller keeps its
                 own object type scope (unchanged legacy behaviour).
     :pending  - configured, but its dependent-field placeholders have no
                 selection yet; the field accepts nothing until the upstream
                 row is picked, so the caller must show no options rather
                 than fall back to a scope Jira would reject.
     {:aql ... :object-schema-id ...}
               - the AQL Jira itself applies to this field: the object scope
                 filter, AND-composed with the resolved issue scope filter.

   The AQL replaces the template's object type: the field configuration
   already defines every object the field accepts."
  [item items fieldconfigs]
  (when-let [{:keys [object-schema-id object-filter issue-scope]}
             (get fieldconfigs (:jira_field item))]
    (let [scope (when-not (cs/blank? issue-scope)
                  (substitute-placeholders issue-scope items))
          clauses (remove cs/blank? [object-filter scope])]
      (cond
        (seq clauses)
        {:aql (if (= 1 (count clauses))
                (first clauses)
                (cs/join " AND " (map #(str "(" % ")") clauses)))
         :object-schema-id object-schema-id}

        ;; configured with an unresolved dependency only
        (not (cs/blank? issue-scope)) :pending

        ;; configured with nothing to filter by
        :else nil))))

(defn- references?
  "Does item's issue scope filter reference changed-field via placeholder?"
  [fieldconfigs item changed-field]
  (let [issue-scope (get-in fieldconfigs [(:jira_field item) :issue-scope])]
    (some #(= changed-field (second %))
          (re-seq placeholder-re (str issue-scope)))))

(defn dependents-of
  "Rows whose issue scope filter references the changed row's field."
  [changed-item items fieldconfigs]
  (filter #(and (not= (:jira_field %) (:jira_field changed-item))
                (references? fieldconfigs % (:jira_field changed-item)))
          items))

(defn all-dependents
  "Transitive closure of dependents-of, breadth-first: a cleared row clears
   its own dependents in turn (A -> B -> C), so a chain never keeps a pick
   that was filtered by a now-cleared upstream. The seen set makes cyclic
   references terminate."
  [changed-item items fieldconfigs]
  (loop [frontier [changed-item]
         seen #{(:jira_field changed-item)}
         acc []]
    (if-let [item (first frontier)]
      (let [deps (remove #(contains? seen (:jira_field %))
                         (dependents-of item items fieldconfigs))]
        (recur (into (vec (rest frontier)) deps)
               (into seen (map :jira_field deps))
               (into acc deps)))
      acc)))
