(ns webapp.events.editor-plugin
  (:require
   [cljs.core :as c]
   [clojure.edn :refer [read-string]]
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
 :editor-plugin->get-run-connection-list
 (fn
   [{:keys [db]} [_]]
   {:db (assoc db :editor-plugin->run-connection-list {:status :loading :data {}}
               :editor-plugin->run-connection-list-selected
               (or (read-string
                    (.getItem js/localStorage "run-connection-list-selected")) nil))
    :fx [[:dispatch [:connections->get-connections
                     {:on-success [:editor-plugin->process-connections]
                      :on-failure [:editor-plugin->connections-error]}]]]}))

;; New event to process connections for editor plugin
(rf/reg-event-fx
 :editor-plugin->process-connections
 (fn [{:keys [db]} [_ connections]]
   {:fx [[:dispatch [::editor-plugin->set-run-connection-list connections]]
         [:dispatch [:editor-plugin->set-filtered-run-connection-list connections]]]}))

;; New error handler for connections
(rf/reg-event-fx
 :editor-plugin->connections-error
 (fn [{:keys [db]} [_ _error]]
   {:db (assoc db :editor-plugin->run-connection-list {:status :error :data {}})}))

(rf/reg-event-fx
 ::editor-plugin->set-run-connection-list
 (fn
   [{:keys [db]} [_ connections]]
   (let [connection-list-cached (read-string (.getItem js/localStorage "run-connection-list-selected"))
         is-cached? (fn [current-connection-name]
                      (not-empty (filter #(= (:name %) current-connection-name) connection-list-cached)))
         connections-parsed (mapv (fn [{:keys [name type subtype status access_schema default_database jira_issue_template_id]}]
                                    {:name name
                                     :type type
                                     :subtype subtype
                                     :status status
                                     :jira_issue_template_id jira_issue_template_id
                                     :access_schema access_schema
                                     :database_name (when (and (= type "database")
                                                               (= subtype "postgres"))
                                                      default_database)
                                     :selected (if (is-cached? name)
                                                 true
                                                 false)})
                                  connections)]
     {:db (assoc db :editor-plugin->run-connection-list {:data connections-parsed :status :ready}
                 :editor-plugin->filtered-run-connection-list connections-parsed)})))

(rf/reg-event-db
 :editor-plugin->set-filtered-run-connection-list
 (fn
   [db [_ connections]]
   (let [connection-list-cached (read-string (.getItem js/localStorage "run-connection-list-selected"))
         is-cached? (fn [current-connection-name]
                      (not-empty (filter #(= (:name %) current-connection-name) connection-list-cached)))
         connections-parsed (mapv (fn [{:keys [name type subtype status selected access_schema default_database jira_issue_template_id]}]
                                    {:name name
                                     :type type
                                     :subtype subtype
                                     :status status
                                     :jira_issue_template_id jira_issue_template_id
                                     :access_schema access_schema
                                     :database_name (when (and (= type "database")
                                                               (= subtype "postgres"))
                                                      default_database)
                                     :selected (if (is-cached? name)
                                                 true
                                                 selected)})
                                  connections)]
     (assoc db :editor-plugin->filtered-run-connection-list connections-parsed))))

(rf/reg-event-fx
 :editor-plugin->toggle-select-run-connection
 (fn
   [{:keys [db]} [_ current-connection-name]]
   (let [connection-list-cached (read-string (.getItem js/localStorage "run-connection-list-selected"))
         connections (:data (:editor-plugin->run-connection-list db))
         current-connection (first (filter #(= (:name %) current-connection-name) (:data (:editor-plugin->run-connection-list db))))
         is-cached? (not-empty (filter #(= (:name %) current-connection-name) connection-list-cached))
         new-connection-list (mapv (fn [connection]
                                     (if (= (:name connection) current-connection-name)
                                       (assoc connection :selected (if is-cached?
                                                                     false
                                                                     (not (:selected connection))))
                                       connection))
                                   connections)
         new-connection-list-selected (if (or (:selected current-connection)
                                              is-cached?)
                                        (remove #(= (:name %) current-connection-name)
                                                (:editor-plugin->run-connection-list-selected db))

                                        (concat (:editor-plugin->run-connection-list-selected db)
                                                [current-connection]))]
     (.setItem js/localStorage "run-connection-list-selected"
               (pr-str new-connection-list-selected))
     (let [primary-connection (first (filter #(:selected %) connections))
           selected-connections new-connection-list-selected
           combined-connections (map :name (concat
                                            (when primary-connection [primary-connection])
                                            selected-connections))]
       {:fx [[:dispatch [:runbooks-plugin->get-runbooks combined-connections]]]
        :db (assoc db :editor-plugin->run-connection-list {:data new-connection-list :status :ready}
                   :editor-plugin->filtered-run-connection-list new-connection-list
                   :editor-plugin->run-connection-list-selected new-connection-list-selected)}))))

(rf/reg-event-fx
 :editor-plugin->run-runbook
 (fn
   [{:keys [db]} [_ {:keys [file-name params connection-name jira_fields cmdb_fields]}]]
   (let [primary-connection (get-in db [:editor :connections :selected])
         selected-db (.getItem js/localStorage "selected-database")
         is-dynamodb? (= (:subtype primary-connection) "dynamodb")
         is-cloudwatch? (= (:subtype primary-connection) "cloudwatch")
         keep-metadata? (get-in db [:editor-plugin :keep-metadata?])
         current-metadatas (get-in db [:editor-plugin :metadata])
         current-metadata-key (get-in db [:editor-plugin :metadata-key])
         current-metadata-value (get-in db [:editor-plugin :metadata-value])
         metadata (conj current-metadatas {:key current-metadata-key :value current-metadata-value})
         env-vars (cond
                    (and is-dynamodb? selected-db)
                    {"envvar:TABLE_NAME" (js/btoa selected-db)}

                    (and is-cloudwatch? selected-db)
                    {"envvar:LOG_GROUP_NAME" (js/btoa selected-db)}

                    :else nil)
         payload (cond-> {:file_name file-name
                          :parameters params
                          :env_vars env-vars
                          :metadata (metadata->json-stringify metadata)}
                   jira_fields (assoc :jira_fields jira_fields)
                   cmdb_fields (assoc :cmdb_fields cmdb_fields))
         on-failure (fn [error-message error]
                      (rf/dispatch [:show-snackbar {:text "Failed to execute runbook"
                                                    :level :error
                                                    :details error}])
                      (rf/dispatch [::editor-plugin->set-script-failure error]))
         on-success (fn [res]
                      (rf/dispatch
                       [:show-snackbar {:level :success
                                        :text "Runbook was executed!"}])
                      (rf/dispatch [::editor-plugin->set-script-success res file-name]))
         base-db (assoc db :editor-plugin->script {:status :loading :data nil})
         fx [[:dispatch [:fetch {:method "POST"
                                 :uri (str "/plugins/runbooks/connections/" connection-name "/exec")
                                 :on-success on-success
                                 :on-failure on-failure
                                 :body payload}]]]]
     (merge {:db base-db
             :fx fx}
            (when-not keep-metadata?
              {:db (-> base-db
                       (assoc-in [:editor-plugin :metadata] [])
                       (assoc-in [:editor-plugin :metadata-key] "")
                       (assoc-in [:editor-plugin :metadata-value] ""))})))))

(rf/reg-event-fx
 :editor-plugin->multiple-connections-run-runbook
 (fn
   [{:keys [db]} [_ runbook-list]]
   (let [on-failure (fn [error exec]
                      (rf/dispatch [::editor-plugin->set-multiple-connections-run-runbook-failure error exec]))
         on-success (fn [res exec]
                      (rf/dispatch [::editor-plugin->set-multiple-connections-run-runbook-success res exec]))
         dispatchs (mapv (fn [runbook]
                           [:dispatch-later {:ms 1000
                                             :dispatch [:fetch {:method "POST"
                                                                :uri (str "/plugins/runbooks/connections/"
                                                                          (:connection-name runbook) "/exec")
                                                                :on-success (fn [res]
                                                                              (on-success res runbook))
                                                                :on-failure (fn [error]
                                                                              (on-failure error runbook))
                                                                :body {:file_name (:file_name runbook)
                                                                       :parameters (:parameters runbook)}}]}])
                         runbook-list)]
     {:db (assoc db :editor-plugin->connections-runbook-list {:data runbook-list :status :running})
      :fx dispatchs})))

(rf/reg-event-fx
 ::editor-plugin->set-multiple-connections-run-runbook-success
 (fn
   [{:keys [db]} [_ data current-runbook]]
   (let [current-runbook-parsed {:connection-name (:connection-name current-runbook)
                                 :subtype (:subtype current-runbook)
                                 :type (:type current-runbook)
                                 :session-id (:session_id data)
                                 :status (if (:has_review data)
                                           :waiting-review
                                           :completed)}
         new-connections-runbook-list (mapv (fn [runbook]
                                              (if (= (:connection-name runbook)
                                                     (:connection-name current-runbook))
                                                current-runbook-parsed
                                                runbook))
                                            (:data (:editor-plugin->connections-runbook-list db)))
         finished? (every? #(or (= :completed (:status %))
                                (= :waiting-review (:status %))
                                (= :error (:status %))) new-connections-runbook-list)]

     {:db (assoc db :editor-plugin->connections-runbook-list {:data new-connections-runbook-list
                                                              :status (if finished?
                                                                        :completed
                                                                        :running)})})))

(rf/reg-event-fx
 ::editor-plugin->set-multiple-connections-run-runbook-failure
 (fn
   [{:keys [db]} [_ data current-runbook]]
   (let [current-runbook-parsed {:connection-name (:connection-name current-runbook)
                                 :subtype (:subtype current-runbook)
                                 :type (:type current-runbook)
                                 :session-id (:session-id data)
                                 :status :error}
         new-connections-runbook-list (mapv (fn [runbook]
                                              (if (= (:connection-name runbook)
                                                     (:connection-name current-runbook))
                                                current-runbook-parsed
                                                runbook))
                                            (:data (:editor-plugin->connections-runbook-list db)))
         finished? (every? #(or (= :completed (:status %))
                                (= :waiting-review (:status %))
                                (= :error (:status %))) new-connections-runbook-list)]

     {:db (assoc db :editor-plugin->connections-runbook-list {:data new-connections-runbook-list
                                                              :status (if finished?
                                                                        :completed
                                                                        :running)})})))

(rf/reg-event-fx
 :editor-plugin->clear-multiple-connections-run-runbook
 (fn
   [{:keys [db]} [_]]
   {:db (assoc db :editor-plugin->connections-runbook-list {:data [] :status :ready})}))

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
     {:db (assoc db :editor-plugin->script {:status :loading :data nil})
      :fx [[:dispatch [:fetch {:method "POST"
                               :uri "/sessions"
                               :on-success on-success
                               :on-failure on-failure
                               :body payload}]]]})))

(rf/reg-event-fx
 :editor-plugin->multiple-connections-exec-script
 (fn
   [{:keys [db]} [_ exec-list]]
   (let [on-failure (fn [error exec]
                      (rf/dispatch [::editor-plugin->set-connection-script-failure error exec]))
         on-success (fn [res exec]
                      (rf/dispatch [::editor-plugin->set-connection-script-success res exec]))
         dispatchs (mapv (fn [exec]
                           [:dispatch-later {:ms 1000
                                             :dispatch [:fetch {:method "POST"
                                                                :uri "/sessions"
                                                                :on-success (fn [res]
                                                                              (on-success res exec))
                                                                :on-failure (fn [error]
                                                                              (on-failure error exec))
                                                                :body {:script (:script exec)
                                                                       :connection (:connection-name exec)
                                                                       :metadata (:metadata exec)}}]}])
                         exec-list)]
     {:db (assoc db :editor-plugin->connections-exec-list {:data exec-list :status :running})
      :fx dispatchs})))

(rf/reg-event-fx
 :editor-plugin->multiple-connections-update-metadata
 (fn
   [{:keys [db]} [_ exec-list]]
   (let [dispatchs (mapv (fn [exec]
                           [:dispatch-later {:ms 1000
                                             :dispatch [:fetch
                                                        {:method "PATCH"
                                                         :uri (str "/sessions/" (:session-id exec) "/metadata")
                                                         :on-success (fn [] false)
                                                         :on-failure (fn [error]
                                                                       (println exec error))
                                                         :body {:metadata
                                                                {"View related sessions"
                                                                 (str (. (. js/window -location) -origin)
                                                                      "/sessions/filtered?id="
                                                                      (cs/join "," (mapv :session-id exec-list)))}}}]}])
                         exec-list)]
     {:fx dispatchs})))

(rf/reg-event-fx
 ::editor-plugin->set-connection-script-success
 (fn
   [{:keys [db]} [_ data current-exec]]
   (let [current-exec-parsed {:connection-name (:connection-name current-exec)
                              :subtype (:subtype current-exec)
                              :type (:type current-exec)
                              :session-id (:session_id data)
                              :status (if (:has_review data)
                                        :waiting-review
                                        :completed)}
         new-connections-exec-list (mapv (fn [exec]
                                           (if (= (:connection-name exec) (:connection-name current-exec))
                                             current-exec-parsed
                                             exec))
                                         (:data (:editor-plugin->connections-exec-list db)))
         finished? (every? #(or (= :completed (:status %))
                                (= :waiting-review (:status %))
                                (= :error (:status %))) new-connections-exec-list)]

     {:db (assoc db :editor-plugin->connections-exec-list {:data new-connections-exec-list
                                                           :status (if finished?
                                                                     :completed
                                                                     :running)})})))

(rf/reg-event-fx
 ::editor-plugin->set-connection-script-failure
 (fn
   [{:keys [db]} [_ data current-exec]]
   (let [current-exec-parsed {:name (:name current-exec)
                              :subtype (:subtype current-exec)
                              :type (:type current-exec)
                              :session-id (:session-id data)
                              :status :error}
         new-connections-exec-list (mapv (fn [exec]
                                           (if (= (:name exec) (:name current-exec))
                                             current-exec-parsed
                                             exec))
                                         (:data (:editor-plugin->connections-exec-list db)))
         finished? (every? #(or (= :completed (:status %))
                                (= :waiting-review (:status %))
                                (= :error (:status %))) new-connections-exec-list)]

     {:db (assoc db :editor-plugin->connections-exec-list {:data new-connections-exec-list
                                                           :status (if finished?
                                                                     :completed
                                                                     :running)})})))

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

(rf/reg-event-fx
 :editor-plugin->set-select-language
 (fn [{:keys [db]} [_ language]]
   {:db (assoc-in db [:editor-plugin->select-language] language)}))

(rf/reg-event-db
 :editor-plugin/toggle-keep-metadata
 (fn [db [_]]
   (update-in db [:editor-plugin :keep-metadata?] not)))

(rf/reg-sub
 :editor-plugin/keep-metadata?
 (fn [db]
   (get-in db [:editor-plugin :keep-metadata?] false)))

;; Submit task event
(rf/reg-event-fx
 :editor-plugin/submit-task
 (fn [{:keys [db]} [_ {:keys [script]}]]
   (let [additional-connections (get-in db [:editor :multi-connections :selected])
         primary-connection (get-in db [:editor :connections :selected])
         all-connections (when primary-connection
                           (cons primary-connection additional-connections))
         multiple-connections? (> (count all-connections) 1)

         has-jira-template-multiple-connections? (some #(not (cs/blank? (:jira_issue_template_id %)))
                                                       all-connections)
         needs-template? (boolean (and primary-connection
                                       (not (cs/blank? (:jira_issue_template_id primary-connection)))))
         connection-type (discover-connection-type primary-connection)
         jira-integration-enabled? (= (-> (get-in db [:jira-integration->details])
                                          :data
                                          :status)
                                      "enabled")
         change-to-tabular? (and (some (partial = connection-type)
                                       ["mysql" "postgres" "sql-server" "oracledb" "mssql" "database"])
                                 (< (count script) 1))
         selected-db (.getItem js/localStorage "selected-database")
         is-dynamodb? (= (:subtype primary-connection) "dynamodb")
         is-cloudwatch? (= (:subtype primary-connection) "cloudwatch")
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
                             (= connection-type "postgres")
                             (not multiple-connections?)) (str "\\set QUIET on\n"
                                                               "\\c " selected-db "\n"
                                                               "\\set QUIET off\n"
                                                               script)
                        (and selected-db
                             (= connection-type "mysql")
                             (not multiple-connections?)) (str "use " selected-db ";\n" script)
                        (and selected-db
                             (= connection-type "mongodb")
                             (not multiple-connections?)) (str "use " selected-db ";\n" script)
                        :else script)]

     (cond
       ;; No connection selected
       (empty? primary-connection)
       {:fx [[:dispatch [:show-snackbar
                         {:level :error
                          :text "You must choose a connection"}]]]}

       ;; Multiple connections with Jira template not allowed
       (and multiple-connections?
            has-jira-template-multiple-connections?
            jira-integration-enabled?)
       {:fx [[:dispatch [:dialog->open
                         {:title "Running in multiple connections not allowed"
                          :action-button? false
                          :text "For now, it's not possible to run commands in multiple connections with Jira Templates activated. Please select just one connection before running your command."}]]]}

       ;; Multiple connections - show execution modal
       multiple-connections?
       {:fx [[:dispatch [:multiple-connection-execution/show-modal
                         (map #(hash-map
                                :connection-name (:name %)
                                :script final-script
                                :metadata (metadata->json-stringify metadata)
                                :env_vars (when (and (= (:subtype %) "dynamodb") selected-db)
                                            {"envvar:TABLE_NAME" (js/btoa selected-db)})
                                :type (:type %)
                                :subtype (:subtype %)
                                :session-id nil
                                :status :ready)
                              all-connections)]]]}

       ;; Single connection with JIRA template
       (and needs-template? jira-integration-enabled?)
       {:fx [[:dispatch [:modal->open
                         {:maxWidth "540px"
                          :custom-on-click-out #(.preventDefault %)
                          :content [loading-jira-templates/main]}]]
             [:dispatch [:jira-templates->get-submit-template
                         (:jira_issue_template_id primary-connection)]]
             [:dispatch-later
              {:ms 1000
               :dispatch [:editor-plugin/check-template-and-show-form
                          {:template-id (:jira_issue_template_id primary-connection)
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
                           :connection-name (:name primary-connection)
                           :metadata (metadata->json-stringify metadata)
                           :env_vars env-vars}]]]}
        (when-not keep-metadata?
          {:db (-> db
                   (assoc-in [:editor-plugin :metadata] [])
                   (assoc-in [:editor-plugin :metadata-key] "")
                   (assoc-in [:editor-plugin :metadata-value] ""))}))))))

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
   (let [connection (get-in db [:editor :connections :selected])
         is-dynamodb? (= (:subtype connection) "dynamodb")
         is-cloudwatch? (= (:subtype connection) "cloudwatch")
         selected-db (.getItem js/localStorage "selected-database")
         env-vars (cond
                    (and is-dynamodb? selected-db)
                    {"envvar:TABLE_NAME" (js/btoa selected-db)}

                    (and is-cloudwatch? selected-db)
                    {"envvar:LOG_GROUP_NAME" selected-db}

                    :else nil)]
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
   (-> db
       (assoc-in [:editor-plugin :metadata] [])
       (assoc-in [:editor-plugin :metadata-key] "")
       (assoc-in [:editor-plugin :metadata-value] ""))))

(rf/reg-event-fx
 :runbooks-plugin/show-jira-form
 (fn [{:keys [db]} [_ {:keys [template-id file-name params connection-name]}]]
   {:fx [[:dispatch [:modal->open
                     {:maxWidth "540px"
                      :custom-on-click-out #(.preventDefault %)
                      :content [loading-jira-templates/main]}]]
         [:dispatch [:jira-templates->get-submit-template template-id]]
         [:dispatch-later
          {:ms 1000
           :dispatch [:runbooks-plugin/check-template-and-show-form
                      {:template-id template-id
                       :file-name file-name
                       :params params
                       :connection-name connection-name}]}]]}))

(rf/reg-event-fx
 :runbooks-plugin/check-template-and-show-form
 (fn [{:keys [db]} [_ {:keys [template-id file-name params connection-name] :as context}]]
   (let [template (get-in db [:jira-templates->submit-template])]
     (cond
       ;; Still loading
       (or (nil? (:data template))
           (= :loading (:status template)))
       {:fx [[:dispatch-later
              {:ms 500
               :dispatch [:runbooks-plugin/check-template-and-show-form
                          {:template-id template-id
                           :file-name file-name
                           :params params
                           :connection-name connection-name}]}]]}

       ;; Ready but with failed CMDB requests
       (and (= :ready (:status template))
            (some :request-failed (get-in template [:data :cmdb_types :items])))
       {:fx [[:dispatch [:jira-templates->handle-cmdb-error
                         (assoc context :flow :runbooks)]]]}

       ;; Ready and needs form
       (needs-form? template)
       {:fx [[:dispatch [:modal->open
                         {:content [prompt-form/main
                                    {:prompts (get-in template [:data :prompt_types :items])
                                     :cmdb-items (get-in template [:data :cmdb_types :items])
                                     :on-submit #(rf/dispatch
                                                  [:runbooks-plugin/handle-template-submit
                                                   {:form-data %
                                                    :file-name file-name
                                                    :params params
                                                    :connection-name connection-name}])}]}]]]}

       ;; Ready and doesn't need form
       :else
       {:fx [[:dispatch [:runbooks-plugin/handle-template-submit
                         {:form-data nil
                          :file-name file-name
                          :params params
                          :connection-name connection-name}]]]}))))

(rf/reg-event-fx
 :runbooks-plugin/handle-template-submit
 (fn [{:keys [db]} [_ {:keys [form-data file-name params connection-name]}]]
   (let [connection (first (filter #(= (:name %) connection-name)
                                   (get-in db [:editor-plugin->run-connection-list :data])))
         is-dynamodb? (= (:subtype connection) "dynamodb")
         is-cloudwatch? (= (:subtype connection) "cloudwatch")
         selected-db (.getItem js/localStorage "selected-database")
         env-vars (cond
                    (and is-dynamodb? selected-db)
                    {"envvar:TABLE_NAME" (js/btoa selected-db)}

                    (and is-cloudwatch? selected-db)
                    {"envvar:LOG_GROUP_NAME" selected-db}

                    :else nil)]
     {:fx [[:dispatch [:modal->close]]
           [:dispatch [:editor-plugin->run-runbook
                       (cond-> {:file-name file-name
                                :params params
                                :connection-name connection-name}
                         env-vars (assoc :env_vars env-vars)
                         (:jira_fields form-data) (assoc :jira_fields (:jira_fields form-data))
                         (:cmdb_fields form-data) (assoc :cmdb_fields (:cmdb_fields form-data)))]]]})))
