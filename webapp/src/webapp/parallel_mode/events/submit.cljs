(ns webapp.parallel-mode.events.submit
  (:require
   [clojure.string :as cs]
   [re-frame.core :as rf]))

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
   (let [parallel-connection-names (set (map :name (get-in db [:parallel-mode :selection :connections])))
         connection-details (get-in db [:connections :details])

         ;; Get fresh connection data
         all-connections (vec (keep #(get connection-details %) parallel-connection-names))

         ;; Check for Jira templates
         has-jira-template? (some #(not (cs/blank? (:jira_issue_template_id %))) all-connections)
         jira-integration-enabled? (= (-> (get-in db [:jira-integration->details])
                                          :data
                                          :status)
                                      "enabled")

         ;; Get metadata
         keep-metadata? (get-in db [:editor-plugin :keep-metadata?])
         current-metadatas (get-in db [:editor-plugin :metadata])
         current-metadata-key (get-in db [:editor-plugin :metadata-key])
         current-metadata-value (get-in db [:editor-plugin :metadata-value])
         metadata (conj current-metadatas {:key current-metadata-key :value current-metadata-value})

         ;; Get selected database (for database connections)
         selected-db (.getItem js/localStorage "selected-database")]

     (cond
       ;; No connections
       (empty? all-connections)
       {:fx [[:dispatch [:show-snackbar
                         {:level :error
                          :text "No connections found"}]]]}

       ;; Jira templates not supported in parallel mode
       (and has-jira-template? jira-integration-enabled?)
       {:fx [[:dispatch [:dialog->open
                         {:title "Jira Templates not supported in Parallel Mode"
                          :action-button? false
                          :text "Jira Templates cannot be used with Parallel Mode. Please disable Jira integration or use single connection mode."}]]]}

       ;; Execute in parallel
       :else
       {:fx [[:dispatch [:parallel-mode/show-execution-preview
                         (map (fn [conn]
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
                                   :status :ready}))
                              all-connections)]]]
        :db (when-not keep-metadata?
              (update db :editor-plugin merge {:metadata []
                                               :metadata-key ""
                                               :metadata-value ""}))}))))

