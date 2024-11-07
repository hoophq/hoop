(ns webapp.guardrails.helpers
  (:require [reagent.core :as r]))

(defn create-empty-rule []
  {:type ""
   :rule ""
   :pattern ""
   :words []
   :selected false})

(defn all-rows-selected? [rules]
  (every? :selected rules))

(defn create-form-state [initial-data]
  {:id (r/atom (or (:id initial-data) ""))
   :name (r/atom (or (:name initial-data) ""))
   :description (r/atom (or (:description initial-data) ""))
   :input (r/atom (or (:input initial-data) [(create-empty-rule)]))
   :output (r/atom (or (:output initial-data) [(create-empty-rule)]))
   :input-pattern (r/atom {})
   :output-pattern (r/atom {})
   :input-words (r/atom {})
   :output-words (r/atom {})
   :input-select (r/atom false)
   :output-select (r/atom false)})

(defn create-form-handlers [state]
  {:on-word-change (fn [words-atom idx value]
                     (swap! words-atom assoc idx value))
   :on-pattern-change (fn [pattern-atom idx value]
                        (swap! pattern-atom assoc idx value))
   :on-rule-field-change (fn [rules-atom idx field value]
                           (swap! rules-atom assoc-in [idx field] value))
   :on-rule-select (fn [rules-atom idx]
                     (swap! rules-atom update-in [idx :selected] not))
   :on-toggle-select (fn [select-state-atom]
                       (reset! select-state-atom (not @select-state-atom)))
   :on-all-rules-select (fn [rules-atom]
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
