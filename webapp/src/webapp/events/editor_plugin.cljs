(ns webapp.events.editor-plugin
  (:require [clojure.core.reducers :as reducers]
            [clojure.string :as string]
            [re-frame.core :as rf]))

(rf/reg-event-fx
 :editor-plugin->get-run-connection-list
 (fn
   [{:keys [db]} [_ current-connection-name]]
   {:db (assoc db :editor-plugin->run-connection-list {:status :loading :data {}}
               :editor-plugin->run-connection-list-selected nil)
    :fx [[:dispatch
          [:fetch {:method "GET"
                   :uri "/connections"
                   :on-success (fn [connections]
                                 (rf/dispatch [::editor-plugin->set-run-connection-list
                                               connections
                                               current-connection-name])
                                 (rf/dispatch [:editor-plugin->set-filtered-run-connection-list
                                               connections
                                               current-connection-name]))}]]]}))

(rf/reg-event-fx
 ::editor-plugin->set-run-connection-list
 (fn
   [{:keys [db]} [_ connections current-connection-name]]
   (let [connections-parsed (mapv (fn [{:keys [name type subtype status access_schema]}]
                                    {:name name
                                     :type type
                                     :subtype subtype
                                     :status status
                                     :access_schema access_schema
                                     :selected (if (= name current-connection-name)
                                                 true
                                                 false)})
                                  connections)]
     {:db (assoc db :editor-plugin->run-connection-list {:data connections-parsed :status :ready})})))

(rf/reg-event-db
 :editor-plugin->set-filtered-run-connection-list
 (fn
   [db [_ connections current-connection-name]]
   (let [connections-parsed (mapv (fn [{:keys [name type subtype status selected access_schema]}]
                                    {:name name
                                     :type type
                                     :subtype subtype
                                     :status status
                                     :access_schema access_schema
                                     :selected (if (= name current-connection-name)
                                                 true
                                                 selected)})
                                  connections)]
     (assoc db :editor-plugin->filtered-run-connection-list connections-parsed))))

(rf/reg-event-fx
 :editor-plugin->toggle-select-run-connection
 (fn
   [{:keys [db]} [_ current-connection-name]]
   (let [connections (:data (:editor-plugin->run-connection-list db))
         current-connection (first (filter #(= (:name %) current-connection-name) (:data (:editor-plugin->run-connection-list db))))
         new-connection-list (mapv (fn [connection]
                                     (if (= (:name connection) current-connection-name)
                                       (assoc connection :selected (not (:selected connection)))
                                       connection))
                                   connections)
         new-filtered-connection-list (mapv (fn [connection]
                                              (if (= (:name connection) current-connection-name)
                                                (assoc connection :selected (not (:selected connection)))
                                                connection))
                                            connections)
         new-connection-list-selected (if (:selected current-connection)
                                        (remove #(= (:name %) current-connection-name)
                                                (:editor-plugin->run-connection-list-selected db))

                                        (concat (:editor-plugin->run-connection-list-selected db)
                                                [current-connection]))]
     {:db (assoc db :editor-plugin->run-connection-list {:data new-connection-list :status :ready}
                 :editor-plugin->filtered-run-connection-list new-filtered-connection-list
                 :editor-plugin->run-connection-list-selected new-connection-list-selected)})))

(rf/reg-event-fx
 :editor-plugin->run-runbook
 (fn
   [{:keys [db]} [_ {:keys [file-name params connection-name]}]]
   (let [payload {:file_name file-name
                  :parameters params}
         on-failure (fn [error-message error]
                      (rf/dispatch [:show-snackbar {:text error-message :level :error}])
                      (rf/dispatch [::editor-plugin->set-script-failure error]))
         on-success (fn [res]
                      (rf/dispatch
                       [:show-snackbar {:level :success
                                        :text "Runbook was executed!"}])
                      (rf/dispatch [::editor-plugin->set-script-success res file-name]))]
     {:db (assoc db :editor-plugin->script (into [] (cons {:status :loading :data nil}
                                                          (:editor-plugin->script db))))
      :fx [[:dispatch [:fetch {:method "POST"
                               :uri (str "/plugins/runbooks/connections/" connection-name "/exec")
                               :on-success on-success
                               :on-failure on-failure
                               :body payload}]]]})))

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
                                              (if (= (:connection-name runbook) (:connection-name current-runbook))
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
   (let [current-runbook-parsed {:name (:name current-runbook)
                                 :subtype (:subtype current-runbook)
                                 :type (:type current-runbook)
                                 :session-id (:session-id data)
                                 :status :error}
         new-connections-runbook-list (mapv (fn [runbook]
                                              (if (= (:name runbook) (:name current-runbook))
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
   [{:keys [db]} [_ {:keys [script connection-name metadata]}]]
   (let [payload {:script script
                  :connection connection-name
                  :metadata metadata}
         on-failure (fn [error]
                      (rf/dispatch [:show-snackbar {:text error :level :error}])
                      (rf/dispatch [::editor-plugin->set-script-failure error]))
         on-success (fn [res]
                      (rf/dispatch
                       [:show-snackbar {:level :success
                                        :text "Script was executed!"}])
                      (rf/dispatch [::editor-plugin->set-script-success res script]))]
     {:db (assoc db :editor-plugin->script (into [] (cons {:status :loading :data nil}
                                                          (:editor-plugin->script db))))
      :fx [[:dispatch [:fetch {:method "POST"
                               :uri (str "/sessions")
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
 (fn
   [{:keys [db]} [_ data script]]
   {:db (assoc db :editor-plugin->script (take 10
                                               (assoc (:editor-plugin->script db) 0
                                                      {:status :success :data (merge data {:script script})})))}))

(rf/reg-event-fx
 ::editor-plugin->set-script-failure
 (fn
   [{:keys [db]} [_ error]]
   {:db (assoc db :editor-plugin->script (take 10
                                               (assoc (:editor-plugin->script db) 0
                                                      {:status :failure :data error})))}))

(rf/reg-event-fx
 :editor-plugin->clear-script
 (fn
   [{:keys [db]} [_ data]]
   {:db (assoc db :editor-plugin->script [])}))

(rf/reg-event-fx
 :editor-plugin->get-connection
 (fn
   [{:keys [db]} [_ connection-name]]
   {:db (assoc db :editor-plugin->current-connection {:loading true :data {}})
    :fx [[:dispatch
          [:fetch {:method "GET"
                   :uri (str "/connections/" connection-name)
                   :on-success (fn [connection]
                                 (rf/dispatch [::editor-plugin->set-current-connection connection]))}]]]}))

(rf/reg-event-fx
 ::editor-plugin->set-current-connection
 (fn
   [{:keys [db]} [_ connection]]
   {:db (assoc db :editor-plugin->current-connection {:loading false :data connection})}))

(rf/reg-event-fx
 :editor-plugin->clear-mysql-schema
 (fn
   [{:keys [db]} [_ data]]
   {:db (assoc db :database-schema {:status :idle
                                    :data {}
                                    :type nil
                                    :raw nil
                                    :schema-tree nil
                                    :indexes-tree nil})}))

(rf/reg-event-fx
 :editor-plugin->get-mysql-schema
 (fn
   [{:keys [db]} [_ connection schema-query index-query]]
   (let [fetch {:method "POST"
                :uri (str "/connections/" (:connection-name connection) "/exec")
                :body {:script schema-query}
                :on-success #(rf/dispatch [:editor-plugin->get-mysql-indexes connection index-query %])}]
     {:fx [[:dispatch [:fetch fetch]]]
      :db (assoc-in db [:database-schema] {:status :loading
                                           :data (assoc (-> db :database-schema :data)
                                                        (:connection-name connection) {:status :loading
                                                                                       :type nil
                                                                                       :raw nil
                                                                                       :schema-tree nil
                                                                                       :indexes-tree nil})
                                           :type nil
                                           :raw nil
                                           :schema-tree nil
                                           :indexes-tree nil})})))

(rf/reg-event-fx
 :editor-plugin->get-mysql-indexes
 (fn
   [{:keys [db]} [_ connection index-query schema-payload]]
   {:fx [[:dispatch [:fetch {:method "POST"
                             :uri (str "/connections/" (:connection-name connection) "/exec")
                             :body {:script index-query}
                             :on-success #(rf/dispatch [:editor-plugin->set-mysql-schema
                                                        {:schema-payload schema-payload
                                                         :indexes-payload %
                                                         :status :success
                                                         :connection connection}])}]]]}))

(defn- parse-sql-to-tree
  "This functions gets a TAB separated DB response string and convert to a hashmap
  Input Example (raw):
  ```
  TABLE_SCHEMA	TABLE_NAME	COLUMN_NAME	DATA_TYPE	IS_NULLABLE
  hoop	agent_config	id	int	NO
  hoop	agent_config	org	varchar	YES
  hoop	tx_events	compacted	int	NO
  ```

  and outputs to:
  ```
  {\"hoop\" {\"agent_config\" {\"id\" {\"int\" {\"NO\" nil}}
                                 \"org\" {\"Varchar\" {\"YES\" nil}}}
               \"tx_events\" {\"compacted\" {\"int\" {\"NO\" nil}}}}}
  ```
  (all values as strings)
  "
  [raw db-type]
  (let [raw-sanitized (cond (= db-type "mssql") (string/replace raw #" " "\t")
                            (= db-type "sql-server-csv") (string/replace raw #" " "\t")
                            :else (string/replace raw #"∞" "\t"))
        outer-trimmed (cond
                        (= db-type "sql-server-csv") (drop-last 2 (drop 2 (string/split raw-sanitized #"\n")))
                        (= db-type "mssql") (drop-last 2 (drop 2 (string/split raw-sanitized #"\n")))
                        :else (drop 1 (string/split raw-sanitized #"\n")))
        inner-split #(string/split % #"\t")
        tree-atom (atom {})
        mount-tree (fn
                     ([] {:previous nil :tree {} :deep [:tree]})
                     ([x y]
                      (swap! tree-atom assoc-in (:deep x) (merge {y nil} (get-in @tree-atom (:deep x))))
                      (merge x {:tree (:tree (assoc-in x (:deep x) {y nil}))
                                :deep (conj (:deep x) y)})))
        _ (mapv #(reducers/fold mount-tree (inner-split %)) outer-trimmed)]
    @tree-atom))

(rf/reg-event-fx
 :editor-plugin->set-mysql-schema
 (fn
   [{:keys [db]} [_ {:keys [schema-payload indexes-payload status connection]}]]
   (let [schema {:status status
                 :data (assoc (-> db :database-schema :data)
                              (:connection-name connection) {:status status
                                                             :type (:connection-type connection)
                                                             :raw (:output schema-payload)
                                                             :schema-tree (if-let [_ (empty? schema-payload)]
                                                                            "Couldn't get database schema"
                                                                            (:tree (parse-sql-to-tree (:output schema-payload)
                                                                                                      (:connection-type connection))))
                                                             :indexes-tree (:tree (parse-sql-to-tree (:output indexes-payload)
                                                                                                     (:connection-type connection)))})
                 :type (:connection-type connection)
                 :raw (:output schema-payload)
                 :schema-tree (if-let [_ (empty? schema-payload)]
                                "Couldn't get database schema"
                                (:tree (parse-sql-to-tree (:output schema-payload)
                                                          (:connection-type connection))))
                 :indexes-tree (:tree (parse-sql-to-tree (:output indexes-payload)
                                                         (:connection-type connection)))}]
     {:db (assoc-in db [:database-schema] schema)})))

(rf/reg-event-fx
 :editor-plugin->get-mongodb-schema
 (fn
   [{:keys [db]} [_ connection schema-query]]
   (let [fetch {:method "POST"
                :uri (str "/connections/" (:connection-name connection) "/exec")
                :body {:script schema-query}
                :on-success (fn [res]
                              (if (= (:output_status res) "failed")
                                (rf/dispatch [:editor-plugin->parse-mongodb-schema
                                              :failure
                                              connection (:output res)])

                                (rf/dispatch [:editor-plugin->parse-mongodb-schema
                                              :success
                                              connection (:output res)])))}]
     {:fx [[:dispatch [:fetch fetch]]]
      :db (assoc-in db [:database-schema] {:status :loading
                                           :data (assoc (-> db :database-schema :data)
                                                        (:connection-name connection) {:status :loading
                                                                                       :raw nil
                                                                                       :type nil
                                                                                       :schema-tree nil
                                                                                       :indexes-tree nil})
                                           :raw nil
                                           :type nil
                                           :schema-tree nil
                                           :indexes-tree nil})})))

(rf/reg-event-fx
 :editor-plugin->parse-mongodb-schema
 (fn
   [_ [_ status connection payload]]
   {:fx [[:dispatch [:editor-plugin->set-mongodb-schema status connection payload]]]}))

(rf/reg-event-fx
 :editor-plugin->set-mongodb-schema
 (fn
   [{:keys [db]} [_ status connection payload]]
   (let [parse-payload (fn [p]
                         (js->clj (.parse js/JSON p) :keywordize-keys true))
         db-schema {:status status
                    :data (assoc (-> db :database-schema :data)
                                 (:connection-name connection) {:status status
                                                                :raw payload
                                                                :schema-tree (if-let [_ (or (empty? payload)
                                                                                            (= status :failure))]
                                                                               "Couldn't get database schema.\nPlease check your session list to see the possible issue."
                                                                               (parse-payload payload))})
                    :type "mongodb"
                    :raw payload
                    :schema-tree (if-let [_ (or (empty? payload)
                                                (= status :failure))]
                                   "Couldn't get database schema.\nPlease check your session list to see the possible issue."
                                   (parse-payload payload))}]
     {:db (assoc-in db [:database-schema] db-schema)})))

(rf/reg-event-fx
 :editor-plugin->set-select-language
 (fn [{:keys [db]} [_ language]]
   {:db (assoc-in db [:editor-plugin->select-language] language)}))
