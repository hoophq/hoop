(ns webapp.parallel-mode.events.submit
  (:require
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.parallel-mode.helpers :as helpers]))

(defn metadata->json-stringify [metadata]
  (->> metadata
       (filter (fn [{:keys [key value]}]
                 (not (or (cs/blank? key) (cs/blank? value)))))
       (map (fn [{:keys [key value]}] {key value}))
       (reduce into {})
       (clj->js)))

(rf/reg-event-fx
 :parallel-mode/submit-task-with-fresh-data
 (fn [{:keys [db]} [_ {:keys [script]}]]
   (js/console.log "🎯 parallel-mode/submit-task-with-fresh-data called" "script:" script)
   (let [parallel-connection-names (set (map :name (get-in db [:parallel-mode :selection :connections])))
         connection-details (get-in db [:connections :details])
         
         ;; Get fresh connection data
         all-connections (vec (keep #(get connection-details %) parallel-connection-names))
         
         jira-integration-enabled? (= (-> (get-in db [:jira-integration->details])
                                          :data
                                          :status)
                                      "enabled")
         
         ;; Pre-validate connections
         {:keys [to-execute pre-failed]} (helpers/split-by-validation all-connections jira-integration-enabled?)
         
         ;; Get metadata
         keep-metadata? (get-in db [:editor-plugin :keep-metadata?])
         current-metadatas (get-in db [:editor-plugin :metadata])
         current-metadata-key (get-in db [:editor-plugin :metadata-key])
         current-metadata-value (get-in db [:editor-plugin :metadata-value])
         metadata (conj current-metadatas {:key current-metadata-key :value current-metadata-value})
         
         ;; Get selected database (for database connections)
         selected-db (.getItem js/localStorage "selected-database")
         
         ;; Build exec list
         build-exec-item (fn [conn status]
                           (let [is-dynamodb? (= (:subtype conn) "dynamodb")
                                 is-cloudwatch? (= (:subtype conn) "cloudwatch")
                                 env-vars (cond
                                            (and is-dynamodb? selected-db)
                                            {"envvar:TABLE_NAME" (js/btoa selected-db)}
                                            
                                            (and is-cloudwatch? selected-db)
                                            {"envvar:LOG_GROUP_NAME" (js/btoa selected-db)}
                                            
                                            :else nil)]
                             {:connection-name (:name conn)
                              :script script
                              :metadata (metadata->json-stringify metadata)
                              :env_vars env-vars
                              :type (:type conn)
                              :subtype (:subtype conn)
                              :session-id nil
                              :status status
                              :error-reason (:pre-validation-error conn)}))
         
         ;; Build exec lists
         to-execute-list (map #(build-exec-item % :queued) to-execute)
         pre-failed-list (map #(build-exec-item % (:pre-validation-error %)) pre-failed)
         all-exec-list (concat to-execute-list pre-failed-list)]
     
     (cond
       ;; No connections
       (empty? all-connections)
       {:fx [[:dispatch [:show-snackbar
                         {:level :error
                          :text "No connections found"}]]]}
       
       ;; All connections failed pre-validation
       (empty? to-execute)
       {:fx [[:dispatch [:dialog->open
                         {:title "Cannot Execute in Parallel Mode"
                          :action-button? false
                          :text "All selected roles have restrictions (Jira Templates, Review Required, or Required Metadata) that prevent parallel execution."}]]]}
       
       ;; Execute immediately (no preview)
       :else
       {:fx [[:dispatch [:parallel-mode/execute-immediately all-exec-list to-execute-list]]]
        :db (when-not keep-metadata?
              (update db :editor-plugin merge {:metadata []
                                               :metadata-key ""
                                               :metadata-value ""}))}))))
