(ns webapp.jira-templates.helpers
  (:require [reagent.core :as r]))

(defn create-empty-mapping-rule []
  {:type ""
   :value ""
   :jira_field ""
   :description ""
   :selected false})

(defn create-empty-prompt []
  {:label ""
   :jira_field ""
   :required true
   :description ""
   :selected false})

(defn- format-mapping-rule [rule]
  (if (empty? (:type rule))
    (create-empty-mapping-rule)
    {:type (:type rule)
     :value (:value rule)
     :jira_field (:jira_field rule)
     :description (:description rule)
     :selected false}))

(defn- format-prompt [prompt]
  (if (empty? (:label prompt))
    (create-empty-prompt)
    {:label (:label prompt)
     :jira_field (:jira_field prompt)
     :required (:required prompt)
     :description (:description prompt)
     :selected false}))

(defn- format-mapping-rules [rules]
  (if (empty? rules)
    [(create-empty-mapping-rule)]
    (mapv format-mapping-rule rules)))

(defn- format-prompts [prompts]
  (if (empty? prompts)
    [(create-empty-prompt)]
    (mapv format-prompt prompts)))

(defn create-form-state [initial-data]
  {:id (r/atom (or (:id initial-data) ""))
   :name (r/atom (or (:name initial-data) ""))
   :description (r/atom (or (:description initial-data) ""))
   :project_key (r/atom (or (:project_key initial-data) ""))
   :issue_type_name (r/atom (or (:issue_type_name initial-data) ""))
   :mapping (r/atom (format-mapping-rules (get-in initial-data [:mapping_types :items])))
   :prompts (r/atom (format-prompts (get-in initial-data [:prompt_types :items])))
   :mapping-select-state (r/atom false)
   :prompts-select-state (r/atom false)})

(defn create-form-handlers [state]
  {:on-mapping-field-change (fn [rules-atom idx field value]
                              (swap! rules-atom assoc-in [idx field] value))

   :on-prompt-field-change (fn [prompts-atom idx field value]
                             (swap! prompts-atom assoc-in [idx field] value))

   :on-mapping-select (fn [rules-atom idx]
                        (swap! rules-atom update-in [idx :selected] not))

   :on-prompt-select (fn [prompts-atom idx]
                       (swap! prompts-atom update-in [idx :selected] not))

   :on-toggle-mapping-select (fn [select-state-atom]
                               (reset! select-state-atom (not @select-state-atom)))

   :on-toggle-prompt-select (fn [select-state-atom]
                              (reset! select-state-atom (not @select-state-atom)))

   :on-toggle-all-mapping (fn [rules-atom]
                            (let [all-selected? (every? :selected @rules-atom)]
                              (swap! rules-atom #(mapv
                                                  (fn [rule]
                                                    (assoc rule :selected (not all-selected?)))
                                                  %))))

   :on-toggle-all-prompts (fn [prompts-atom]
                            (let [all-selected? (every? :selected @prompts-atom)]
                              (swap! prompts-atom #(mapv
                                                    (fn [prompt]
                                                      (assoc prompt :selected (not all-selected?)))
                                                    %))))

   :on-mapping-delete (fn [rules-atom]
                        (let [filtered-rules (vec (remove :selected @rules-atom))]
                          (reset! rules-atom
                                  (if (empty? filtered-rules)
                                    [(create-empty-mapping-rule)]
                                    filtered-rules))))

   :on-prompt-delete (fn [prompts-atom]
                       (let [filtered-prompts (vec (remove :selected @prompts-atom))]
                         (reset! prompts-atom
                                 (if (empty? filtered-prompts)
                                   [(create-empty-prompt)]
                                   filtered-prompts))))

   :on-mapping-add (fn [rules-atom]
                     (swap! rules-atom conj (create-empty-mapping-rule)))

   :on-prompt-add (fn [prompts-atom]
                    (swap! prompts-atom conj (create-empty-prompt)))})

(defn remove-empty-mapping [mappings]
  (remove (fn [rule]
            (or (empty? (:type rule))
                (empty? (:value rule))
                (empty? (:jira_field rule))))
          mappings))

(defn remove-empty-prompts [prompts]
  (remove (fn [prompt]
            (or (empty? (:label prompt))
                (empty? (:jira_field prompt))))
          prompts))

(defn prepare-payload [state]
  {:id @(:id state)
   :name @(:name state)
   :description @(:description state)
   :project_key @(:project_key state)
   :issue_type_name @(:issue_type_name state)
   :mapping_types {:items (vec (remove-empty-mapping @(:mapping state)))}
   :prompt_types {:items (vec (remove-empty-prompts @(:prompts state)))}})
