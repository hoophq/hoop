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
   :field_type ""
   :field_options []
   :select-input-value ""
   :description ""
   :timestamp (.now js/Date)
   :selected false})

(defn create-empty-cmdb []
  {:label ""
   :value ""
   :jira_field ""
   :required true
   :description ""
   :jira_object_type ""
   :jira_object_schema_id ""
   :timestamp (.now js/Date)
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
     :field_type (:field_type prompt)
     :field_options (mapv #(into {} {"value" % "label" %}) (:field_options prompt))
     :select-input-value ""
     :required (:required prompt)
     :timestamp (:timestamp prompt)
     :description (:description prompt)
     :selected false}))

(defn- format-cmdb [cmdb]
  (if (empty? (:label cmdb))
    (create-empty-cmdb)
    {:label (:label cmdb)
     :value (:value cmdb)
     :jira_field (:jira_field cmdb)
     :required (:required cmdb)
     :description (:description cmdb)
     :jira_object_type (:jira_object_type cmdb)
     :jira_object_schema_id (:jira_object_schema_id cmdb)
     :timestamp (:timestamp cmdb)
     :selected false}))

(defn- format-mapping-rules [rules]
  (if (empty? rules)
    [(create-empty-mapping-rule)]
    (mapv format-mapping-rule rules)))

(defn- format-prompts [prompts]
  (if (empty? prompts)
    [(create-empty-prompt)]
    (mapv format-prompt prompts)))

(defn- format-cmdbs [cmdbs]
  (if (empty? cmdbs)
    [(create-empty-cmdb)]
    (mapv format-cmdb cmdbs)))

(defn create-form-state [initial-data]
  {:id (r/atom (or (:id initial-data) ""))
   :name (r/atom (or (:name initial-data) ""))
   :description (r/atom (or (:description initial-data) ""))
   :project_key (r/atom (or (:project_key initial-data) ""))
   :request_type_id (r/atom (or (:request_type_id initial-data) ""))
   :issue_transition_name_on_close (r/atom (or (:issue_transition_name_on_close initial-data) ""))
   :mapping (r/atom (format-mapping-rules (get-in initial-data [:mapping_types :items])))
   :prompts (r/atom (format-prompts (get-in initial-data [:prompt_types :items])))
   :cmdb (r/atom (format-cmdbs (get-in initial-data [:cmdb_types :items])))
   :mapping-select-state (r/atom false)
   :prompts-select-state (r/atom false)
   :cmdb-select-state (r/atom false)})

(defn create-form-handlers [state]
  {:on-mapping-field-change (fn [rules-atom idx field value]
                              (swap! rules-atom assoc-in [idx field] value))

   :on-prompt-field-change (fn [prompts-atom idx field value]
                             (swap! prompts-atom assoc-in [idx field] value))

   :on-cmdb-field-change (fn [cmdbs-atom idx field value]
                           (swap! cmdbs-atom assoc-in [idx field] value))

   :on-mapping-select (fn [rules-atom idx]
                        (swap! rules-atom update-in [idx :selected] not))

   :on-prompt-select (fn [prompts-atom idx]
                       (swap! prompts-atom update-in [idx :selected] not))

   :on-cmdb-select (fn [cmdbs-atom idx]
                     (swap! cmdbs-atom update-in [idx :selected] not))

   :on-toggle-mapping-select (fn [select-state-atom]
                               (reset! select-state-atom (not @select-state-atom)))

   :on-toggle-prompt-select (fn [select-state-atom]
                              (reset! select-state-atom (not @select-state-atom)))

   :on-toggle-cmdb-select (fn [select-state-atom]
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

   :on-toggle-all-cmdb (fn [cmdbs-atom]
                         (let [all-selected? (every? :selected @cmdbs-atom)]
                           (swap! cmdbs-atom #(mapv
                                               (fn [cmdb]
                                                 (assoc cmdb :selected (not all-selected?)))
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

   :on-cmdb-delete (fn [cmdbs-atom]
                     (let [filtered-cmdbs (vec (remove :selected @cmdbs-atom))]
                       (reset! cmdbs-atom
                               (if (empty? filtered-cmdbs)
                                 [(create-empty-cmdb)]
                                 filtered-cmdbs))))

   :on-mapping-add (fn [rules-atom]
                     (swap! rules-atom conj (create-empty-mapping-rule)))

   :on-prompt-add (fn [prompts-atom]
                    (swap! prompts-atom conj (create-empty-prompt)))

   :on-cmdb-add (fn [cmdbs-atom]
                  (swap! cmdbs-atom conj (create-empty-cmdb)))})

(defn remove-empty-mapping [mappings]
  (remove (fn [rule]
            (or (empty? (:type rule))
                (empty? (:value rule))
                (empty? (:jira_field rule))))
          mappings))

(defn remove-empty-prompts [prompts]
  (map (fn [prompt]
         (if (= "select" (:field_type prompt))
           (update prompt :field_options #(mapv (fn [option] (get option "value")) %))
           prompt))
       (remove (fn [prompt]
                 (or (empty? (:label prompt))
                     (empty? (:field_type prompt))
                     (empty? (:jira_field prompt))
                     (when (= "select" (:field_type prompt))
                       (empty? (:field_options prompt)))))
               prompts)))

(defn remove-empty-cmdb [cmdbs]
  (remove (fn [cmdb]
            (or (empty? (:label cmdb))
                (empty? (:jira_field cmdb))
                (empty? (:jira_object_type cmdb))))
          cmdbs))

(defn prepare-payload [state]
  {:id @(:id state)
   :name @(:name state)
   :description @(:description state)
   :project_key @(:project_key state)
   :request_type_id @(:request_type_id state)
   :issue_transition_name_on_close @(:issue_transition_name_on_close state)
   :mapping_types {:items (vec (remove-empty-mapping @(:mapping state)))}
   :prompt_types {:items (vec (remove-empty-prompts @(:prompts state)))}
   :cmdb_types {:items (vec (remove-empty-cmdb @(:cmdb state)))}})
