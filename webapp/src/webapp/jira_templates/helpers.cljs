(ns webapp.jira-templates.helpers
  (:require [reagent.core :as r]))

(defn create-empty-rule []
  {:type ""
   :value ""
   :jira_field ""
   :description ""
   :selected false})

(defn- format-rule [rule]
  (if (empty? (:type rule))
    (create-empty-rule)
    {:type (:type rule)
     :value (:value rule)
     :jira_field (:jira_field rule)
     :description (:description rule)
     :selected false}))

(defn- format-rules [rules]
  (if (empty? rules)
    [(create-empty-rule)]
    (mapv format-rule rules)))

(defn create-form-state [initial-data]
  {:id (r/atom (or (:id initial-data) ""))
   :name (r/atom (or (:name initial-data) ""))
   :description (r/atom (or (:description initial-data) ""))
   :jira_template (r/atom (format-rules (:jira_template initial-data)))
   :select-state (r/atom false)})

(defn create-form-handlers [state]
  {:on-rule-field-change (fn [rules-atom idx field value]
                           (swap! rules-atom assoc-in [idx field] value))

   :on-rule-select (fn [rules-atom idx]
                     (swap! rules-atom update-in [idx :selected] not))

   :on-toggle-select (fn [select-state-atom]
                       (reset! select-state-atom (not @select-state-atom)))

   :on-toggle-all (fn [rules-atom]
                    (let [all-selected? (every? :selected @rules-atom)]
                      (swap! rules-atom #(mapv
                                          (fn [rule]
                                            (assoc rule :selected (not all-selected?)))
                                          %))))

   :on-rules-delete (fn [rules-atom]
                      (let [filtered-rules (vec (remove :selected @rules-atom))]
                        (reset! rules-atom
                                (if (empty? filtered-rules)
                                  [(create-empty-rule)]
                                  filtered-rules))))

   :on-rule-add (fn [rules-atom]
                  (swap! rules-atom conj (create-empty-rule)))})
