(ns webapp.events.editor-plugin
  (:require
   [cljs.core :as c]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.jira-templates.loading-jira-templates :as loading-jira-templates]
   [webapp.jira-templates.prompt-form :as prompt-form]))

(defn discover-connection-type [connection]
  (cond
    (not (cs/blank? (:subtype connection))) (:subtype connection)
    (not (cs/blank? (:icon_name connection))) (:icon_name connection)
    :else (:type connection)))

(defn metadata->json-stringify
  [metadata]
  (->> metadata
       (filter (fn [{:keys [key value]}]
                 (not (or (cs/blank? key) (cs/blank? value)))))
       (map (fn [{:keys [key value]}] {key value}))
       (reduce into {})
       (clj->js)))

(rf/reg-event-fx
 :editor-plugin->exec-script
 (fn
   [{:keys [db]} [_ {:keys [script env_vars connection-name metadata jira_fields]}]]
   (let [payload {:script script
                  :env_vars env_vars
                  :connection connection-name
                  :metadata metadata
                  :jira_fields jira_fields}
         on-failure (fn [error]
                      (rf/dispatch [:show-snackbar {:text "Failed to execute script"
                                                    :level :error
                                                    :details error}])
                      (rf/dispatch [::editor-plugin->set-script-failure error]))
         on-success (fn [res]
                      (rf/dispatch
                       [:show-snackbar {:level :success
                                        :text "Script was executed!"}])
                      (rf/dispatch [::editor-plugin->set-script-success res script]))]
     {:db (assoc-in db [:editor-plugin->script] {:status :loading})
      :fx [[:dispatch [:fetch {:method "POST"
                               :uri "/sessions"
                               :on-success on-success
                               :on-failure on-failure
                               :body payload}]]]})))

(rf/reg-event-fx
 :editor-plugin->clear-connection-script
 (fn
   [{:keys [db]} [_]]
   {:db (assoc db :editor-plugin->connections-exec-list {:data [] :status :ready})}))

(rf/reg-event-fx
 ::editor-plugin->set-script-success
 (fn [{:keys [db]} [_ data script]]
   {:db (assoc-in db [:editor-plugin->script] {:status :success
                                               :data (merge data {:script script})})}))

(rf/reg-event-fx
 ::editor-plugin->set-script-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:editor-plugin->script] {:status :failure :data error})}))

(rf/reg-event-fx
 :editor-plugin->clear-script
 (fn [{:keys [db]} [_]]
   {:db (assoc-in db [:editor-plugin->script] nil)}))

(rf/reg-event-db
 :editor-plugin/toggle-keep-metadata
 (fn [db [_]]
   (update-in db [:editor-plugin :keep-metadata?] not)))

(rf/reg-sub
 :editor-plugin/keep-metadata?
 (fn [db]
   (get-in db [:editor-plugin :keep-metadata?] false)))

(rf/reg-event-fx
 :editor-plugin/submit-task
 (fn [{:keys [db]} [_ context]]
   ;; Check if parallel mode is active
   (let [parallel-connections (get-in db [:parallel-mode :selection :connections])
         parallel-mode? (>= (count parallel-connections) 2)]

     (if parallel-mode?
       ;; Use parallel mode execution
       {:fx [[:dispatch [:connections->get-multiple-by-names
                         (map :name parallel-connections)
                         [:parallel-mode/submit-task-with-fresh-data context]
                         [:editor-plugin/submit-task-connection-error]]]]}

       ;; Use legacy single/multi connection logic
       (let [primary-name (get-in db [:editor :connections :selected :name])
             multi-names (map :name (get-in db [:editor :multi-connections :selected]))
             all-names (remove nil? (cons primary-name multi-names))]

         (if (empty? all-names)
           {:fx [[:dispatch [:show-snackbar {:level :error :text "You must choose a connection"}]]]}
           {:fx [[:dispatch [:connections->get-multiple-by-names
                             all-names
                             [:editor-plugin/submit-task-with-fresh-data context]
                             [:editor-plugin/submit-task-connection-error]]]]}))))))

(rf/reg-event-fx
 :editor-plugin/submit-task-with-fresh-data
 (fn [{:keys [db]} [_ {:keys [script]}]]
   (let [primary-name (get-in db [:editor :connections :selected :name])
         connection-details (get-in db [:connections :details])

         fresh-primary-connection (get connection-details primary-name)
         needs-template? (boolean (and fresh-primary-connection
                                       (not (cs/blank? (:jira_issue_template_id fresh-primary-connection)))))
         connection-type (discover-connection-type fresh-primary-connection)
         jira-integration-enabled? (= (-> (get-in db [:jira-integration->details])
                                          :data
                                          :status)
                                      "enabled")
         change-to-tabular? (and (some (partial = connection-type)
                                       ["mysql" "postgres" "sql-server" "oracledb" "mssql" "database"])
                                 (< (count script) 1))
         selected-db (.getItem js/localStorage "selected-database")
         is-dynamodb? (= (:subtype fresh-primary-connection) "dynamodb")
         is-cloudwatch? (= (:subtype fresh-primary-connection) "cloudwatch")
         env-vars (cond
                    (and is-dynamodb? selected-db)
                    {"envvar:TABLE_NAME" (js/btoa selected-db)}

                    (and is-cloudwatch? selected-db)
                    {"envvar:LOG_GROUP_NAME" (js/btoa selected-db)}

                    :else nil)
         keep-metadata? (get-in db [:editor-plugin :keep-metadata?])
         current-metadatas (get-in db [:editor-plugin :metadata])
         current-metadata-key (get-in db [:editor-plugin :metadata-key])
         current-metadata-value (get-in db [:editor-plugin :metadata-value])
         metadata (conj current-metadatas {:key current-metadata-key :value current-metadata-value})
         final-script (cond
                        (and selected-db
                             (= connection-type "postgres")) (str "\\set QUIET on\n"
                                                                  "\\c " selected-db "\n"
                                                                  "\\set QUIET off\n"
                                                                  script)
                        (and selected-db
                             (= connection-type "mssql")) (str "SET NOCOUNT ON;\n"
                                                               "USE " selected-db ";\n"
                                                               script)
                        (and selected-db
                             (= connection-type "mysql")) (str "use " selected-db ";\n" script)
                        (and selected-db
                             (= connection-type "mongodb")) (str "use " selected-db ";\n" script)
                        :else script)]

     (cond
       ;; No connection selected (should not happen due to previous check)
       (empty? fresh-primary-connection)
       {:fx [[:dispatch [:show-snackbar
                         {:level :error
                          :text "Connection not found"}]]]}

       ;; Single connection with JIRA template
       (and needs-template? jira-integration-enabled?)
       {:fx [[:dispatch [:modal->open
                         {:maxWidth "540px"
                          :custom-on-click-out #(.preventDefault %)
                          :content [loading-jira-templates/main]}]]
             [:dispatch [:jira-templates->get-submit-template
                         (:jira_issue_template_id fresh-primary-connection)]]
             [:dispatch-later
              {:ms 1000
               :dispatch [:editor-plugin/check-template-and-show-form
                          {:template-id (:jira_issue_template_id fresh-primary-connection)
                           :script final-script
                           :metadata metadata
                           :env_vars env-vars
                           :keep-metadata? keep-metadata?}]}]]}

       ;; Single connection direct execution
       :else
       (merge
        {:fx [(when change-to-tabular?
                [:dispatch [:set-tab-tabular]])
              [:dispatch [:editor-plugin->exec-script
                          {:script final-script
                           :connection-name (:name fresh-primary-connection)
                           :metadata (metadata->json-stringify metadata)
                           :env_vars env-vars}]]]}
        (when-not keep-metadata?
          {:db (update db :editor-plugin merge {:metadata []
                                                :metadata-key ""
                                                :metadata-value ""})}))))))

;; Error handler for connection loading failures
(rf/reg-event-fx
 :editor-plugin/submit-task-connection-error
 (fn [_ [_ _error]]
   {:fx [[:dispatch [:show-snackbar
                     {:level :error
                      :text "Failed to verify connections. Please try again."}]]]}))

(defn- needs-form? [template]
  (let [has-prompts? (seq (get-in template [:data :prompt_types :items]))
        has-cmdb? (when-let [cmdb-items (get-in template [:data :cmdb_types :items])]
                    (some (fn [{:keys [value jira_values]}]
                            (when (and value jira_values)
                              (not-any? #(= value (:name %)) jira_values)))
                          cmdb-items))]
    (or has-prompts? has-cmdb?)))

;; Helper event for template checking
(rf/reg-event-fx
 :editor-plugin/check-template-and-show-form
 (fn [{:keys [db]} [_ {:keys [template-id script metadata env_vars keep-metadata?] :as context}]]
   (let [template (get-in db [:jira-templates->submit-template])]
     (cond
       ;; Still loading
       (or (nil? (:data template))
           (= :loading (:status template)))
       {:fx [[:dispatch-later
              {:ms 500
               :dispatch [:editor-plugin/check-template-and-show-form
                          {:template-id template-id
                           :script script
                           :metadata metadata
                           :env_vars env_vars
                           :keep-metadata? keep-metadata?}]}]]}

       ;; Ready but with failed CMDB requests
       (and (= :ready (:status template))
            (some :request-failed (get-in template [:data :cmdb_types :items])))
       {:fx [[:dispatch [:jira-templates->handle-cmdb-error
                         (assoc context :flow :editor)]]]}

       ;; Ready and needs form
       (needs-form? template)
       {:fx [[:dispatch [:modal->open
                         {:content [prompt-form/main
                                    {:prompts (get-in template [:data :prompt_types :items])
                                     :cmdb-items (get-in template [:data :cmdb_types :items])
                                     :on-submit #(rf/dispatch
                                                  [:editor-plugin/handle-template-submit
                                                   {:form-data %
                                                    :script script
                                                    :metadata metadata
                                                    :env_vars env_vars
                                                    :keep-metadata? keep-metadata?}])}]}]]]}

       ;; Ready and doesn't need form
       :else
       {:fx [[:dispatch [:editor-plugin/handle-template-submit
                         {:form-data nil
                          :script script
                          :metadata metadata
                          :env_vars env_vars
                          :keep-metadata? keep-metadata?}]]]}))))

;; Helper event for template submission
(rf/reg-event-fx
 :editor-plugin/handle-template-submit
 (fn [{:keys [db]} [_ {:keys [form-data script metadata env_vars keep-metadata?]}]]
   (let [connection (get-in db [:editor :connections :selected])]
     {:fx [[:dispatch [:modal->close]]
           [:dispatch [:editor-plugin->exec-script
                       (cond-> {:script script
                                :connection-name (:name connection)
                                :metadata (metadata->json-stringify metadata)
                                :env_vars env_vars}
                         (:jira_fields form-data) (assoc :jira_fields (:jira_fields form-data))
                         (:cmdb_fields form-data) (assoc :cmdb_fields (:cmdb_fields form-data)))]]
           (when-not keep-metadata?
             [:dispatch [:editor-plugin/clear-metadata]])]})))

;; Helper event to clear metadata
(rf/reg-event-db
 :editor-plugin/clear-metadata
 (fn [db _]
   (update db :editor-plugin merge {:metadata []
                                    :metadata-key ""
                                    :metadata-value ""})))
