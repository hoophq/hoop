(ns webapp.guardrails.helpers
  (:require [reagent.core :as r]))

(def preset-patterns
  {"(?i)\\bSELECT\\b(?!.*\\bWHERE\\b).*" "require-where"
   "(?i)(?:DELETE[[:space:]]+FROM[[:space:]]+(?:\\w+\\.)*\\w+(?![[:space:]]+WHERE))|(?:UPDATE[[:space:]]+(?:\\w+\\.)*\\w+[[:space:]]+SET(?![[:space:]]+.*[[:space:]]+WHERE))" "require-where-delete-update"})

(def preset-words
  {(str ["password"]) "block-password"})

(defn create-empty-rule []
  {:type ""
   :rule ""
   :pattern ""
   :words []
   :selected false})

(defn- identify-preset [type pattern words]
  (cond
    ;; Para pattern match, verifica se é um preset conhecido
    (and (= type "pattern-match")
         (not (empty? pattern)))
    (get preset-patterns pattern "custom-rule")

    ;; Para deny word, verifica se é um preset conhecido
    (and (= type "deny-word")
         (not (empty? words)))
    (get preset-words (str words) "custom-rule")

    ;; Se tem tipo mas não é preset, é custom
    (not (empty? type))
    "custom-rule"

    ;; Caso contrário, vazio
    :else
    ""))

(defn- format-rule [rule]
  (if (empty? (:type rule))
    (create-empty-rule)
    (let [type (:type rule)
          pattern (:pattern rule)
          words (:words rule)
          preset-rule (identify-preset type pattern words)]
      {:type type
       :rule preset-rule
       :pattern (or pattern "")
       :words (or words [])
       :selected false})))

(defn- format-rules [rules]
  (if (empty? rules)
    [(create-empty-rule)]
    (mapv format-rule rules)))

(defn- create-pattern-state [rules]
  (into {} (map-indexed (fn [idx rule]
                          [idx (:pattern rule)])
                        rules)))

(defn- create-words-state [rules]
  (into {} (map-indexed (fn [idx _] [idx ""]) rules)))

(defn all-rows-selected? [rules]
  (every? :selected rules))

(defn create-form-state [initial-data]
  (let [input-rules (format-rules (:input initial-data))
        output-rules (format-rules (:output initial-data))]
    {:id (r/atom (or (:id initial-data) ""))
     :name (r/atom (or (:name initial-data) ""))
     :description (r/atom (or (:description initial-data) ""))
     :input (r/atom input-rules)
     :output (r/atom output-rules)
     :input-pattern (r/atom (create-pattern-state input-rules))
     :output-pattern (r/atom (create-pattern-state output-rules))
     :input-words (r/atom (create-words-state input-rules))
     :output-words (r/atom (create-words-state output-rules))
     :input-select (r/atom false)
     :output-select (r/atom false)}))

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
