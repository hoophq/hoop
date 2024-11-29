(ns webapp.events.editor-plugin
  (:require [clojure.edn :refer [read-string]]
            [re-frame.core :as rf]))

(rf/reg-event-fx
 :editor-plugin->get-run-connection-list
 (fn
   [{:keys [db]} [_]]
   {:db (assoc db :editor-plugin->run-connection-list {:status :loading :data {}}
               :editor-plugin->run-connection-list-selected
               (or (read-string
                    (.getItem js/localStorage "run-connection-list-selected")) nil))
    :fx [[:dispatch
          [:fetch {:method "GET"
                   :uri "/connections"
                   :on-success (fn [connections]
                                 (rf/dispatch [::editor-plugin->set-run-connection-list
                                               connections])
                                 (rf/dispatch [:editor-plugin->set-filtered-run-connection-list
                                               connections])
                                 (when (and (= (count connections) 1)
                                            (empty? (read-string
                                                     (.getItem js/localStorage "run-connection-list-selected"))))
                                   (rf/dispatch [:editor-plugin->toggle-select-run-connection (:name (first connections))])))}]]]}))

(rf/reg-event-fx
 ::editor-plugin->set-run-connection-list
 (fn
   [{:keys [db]} [_ connections]]
   (let [connection-list-cached (read-string (.getItem js/localStorage "run-connection-list-selected"))
         is-cached? (fn [current-connection-name]
                      (not-empty (filter #(= (:name %) current-connection-name) connection-list-cached)))
         connections-parsed (mapv (fn [{:keys [name type subtype status access_schema secret]}]
                                    {:name name
                                     :type type
                                     :subtype subtype
                                     :status status
                                     :access_schema access_schema
                                     :database_name (when (and (= type "database")
                                                               (= subtype "postgres"))
                                                      (js/atob (:envvar:DB secret)))
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
         connections-parsed (mapv (fn [{:keys [name type subtype status selected access_schema secret]}]
                                    {:name name
                                     :type type
                                     :subtype subtype
                                     :status status
                                     :access_schema access_schema
                                     :database_name (when (and (= type "database")
                                                               (= subtype "postgres"))
                                                      (js/atob (:envvar:DB secret)))
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
     {:fx [[:dispatch [:runbooks-plugin->get-runbooks (mapv #(:name %) new-connection-list-selected)]]]
      :db (assoc db :editor-plugin->run-connection-list {:data new-connection-list :status :ready}
                 :editor-plugin->filtered-run-connection-list new-connection-list
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
                                                      {:status :success :data (merge data
                                                                                     {:script script})})))}))

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
 :editor-plugin->set-select-language
 (fn [{:keys [db]} [_ language]]
   {:db (assoc-in db [:editor-plugin->select-language] language)}))

;; ___________________________________________________________________________________

;; Função auxiliar para processar o schema retornado pela API
(defn- process-schema [schema-data]
  (let [schemas (:schemas schema-data)]
    (reduce (fn [acc schema]
              (let [schema-name (:name schema)
                    tables (reduce (fn [table-acc table]
                                     (assoc table-acc (:name table)
                                            (reduce (fn [col-acc column]
                                                      (assoc col-acc (:name column)
                                                             {(:type column)
                                                              {"nullable" (:nullable column)
                                                               "is_primary_key" (:is_primary_key column)
                                                               "is_foreign_key" (:is_foreign_key column)}}))
                                                    {}
                                                    (:columns table))))
                                   {}
                                   (:tables schema))]
                (assoc acc schema-name tables)))
            {}
            schemas)))

;; Função auxiliar para processar os índices
(defn- process-indexes [schema-data]
  (let [schemas (:schemas schema-data)]
    (reduce (fn [acc schema]
              (let [schema-name (:name schema)
                    tables (reduce (fn [table-acc table]
                                     (assoc table-acc (:name table)
                                            (reduce (fn [idx-acc index]
                                                      (assoc idx-acc (:name index)
                                                             (reduce (fn [col-acc column]
                                                                       (assoc col-acc column
                                                                              {"is_unique" (:is_unique index)
                                                                               "is_primary" (:is_primary index)}))
                                                                     {}
                                                                     (:columns index))))
                                                    {}
                                                    (:indexes table))))
                                   {}
                                   (:tables schema))]
                (assoc acc schema-name tables)))
            {}
            schemas)))

(rf/reg-event-fx
 :editor-plugin->handle-multi-database-schema
 (fn [{:keys [db]} [_ connection]]
   (let [current-connection-data (get-in db [:database-schema :data (:connection-name connection)])
         selected-db (.getItem js/localStorage "selected-database")]
     (if (and selected-db (:databases current-connection-data))
       ;; Se temos database selecionada e lista de databases, buscar o schema
       {:fx [[:dispatch [:editor-plugin->get-multi-database-schema
                         connection
                         selected-db
                         (:databases current-connection-data)]]]}
       ;; Se não, buscar primeiro a lista de databases
       {:fx [[:dispatch [:editor-plugin->get-multi-databases connection]]]}))))

(rf/reg-event-fx
 :editor-plugin->get-multi-databases
 (fn [{:keys [db]} [_ connection]]
   {:db (-> db
            (assoc-in [:database-schema :current-connection] (:connection-name connection))
            (assoc-in [:database-schema :data (:connection-name connection) :status] :loading))
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/connections/" (:connection-name connection) "/databases")
                             :on-success (fn [response]
                                           (let [selected-db (.getItem js/localStorage "selected-database")]
                                          ;; Se tiver uma database selecionada, já busca seu schema
                                             (when selected-db
                                               (rf/dispatch [:editor-plugin->get-multi-database-schema
                                                             connection
                                                             selected-db
                                                             (:databases response)]))
                                          ;; Sempre atualiza a lista de databases
                                             (rf/dispatch [:editor-plugin->set-multi-databases
                                                           connection
                                                           (:databases response)])))}]]]}))

(rf/reg-event-db
 :editor-plugin->set-multi-databases
 (fn [db [_ connection databases]]
   (assoc-in db [:database-schema :data (:connection-name connection) :databases] databases)))

(rf/reg-event-fx
 :editor-plugin->get-multi-database-schema
 (fn [{:keys [db]} [_ connection database databases]]
   {:db (-> db
            (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :loading)
            (assoc-in [:database-schema :data (:connection-name connection) :databases] databases))
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/connections/" (:connection-name connection) "/schema?database=" database)
                             :on-success #(rf/dispatch [:editor-plugin->set-multi-database-schema
                                                        {:schema-payload %
                                                         :database database
                                                         :databases databases
                                                         :status :success
                                                         :database-schema-status :success
                                                         :connection connection}])}]]]}))

(rf/reg-event-fx
 :editor-plugin->set-multi-database-schema
 (fn [{:keys [db]} [_ {:keys [schema-payload database databases status database-schema-status connection]}]]
   (let [is-mongodb? (= (:type connection) "mongodb")
         schema {:status status
                 :data (assoc (-> db :database-schema :data)
                              (:connection-name connection)
                              {:status status
                               :database-schema-status database-schema-status
                               :type (:type connection)
                               :raw schema-payload
                               :schema-tree (process-schema schema-payload)
                             ;; Só processa índices se não for MongoDB
                               :indexes-tree (when-not is-mongodb?
                                               (process-indexes schema-payload))
                               :current-database database
                               :databases databases})
                 :type (:type connection)
                 :raw schema-payload
                 :schema-tree (process-schema schema-payload)
                 :indexes-tree (when-not is-mongodb?
                                 (process-indexes schema-payload))
                 :current-database database
                 :databases databases}]
     {:db (assoc-in db [:database-schema] schema)})))

(rf/reg-event-fx
 :editor-plugin->handle-database-schema
 (fn [{:keys [db]} [_ connection]]
   {:db (-> db
            (assoc-in [:database-schema :current-connection] (:connection-name connection))
            (assoc-in [:database-schema :data (:connection-name connection) :status] :loading))
    :fx [[:dispatch [:editor-plugin->get-database-schema connection]]]}))

(rf/reg-event-fx
 :editor-plugin->get-database-schema
 (fn [{:keys [db]} [_ connection]]
   {:db (assoc-in db [:database-schema :data (:connection-name connection) :database-schema-status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/connections/" (:connection-name connection) "/schema")
                             :on-success #(rf/dispatch [:editor-plugin->set-database-schema
                                                        {:schema-payload %
                                                         :status :success
                                                         :database-schema-status :success
                                                         :connection connection}])}]]]}))

(rf/reg-event-fx
 :editor-plugin->set-database-schema
 (fn [{:keys [db]} [_ {:keys [schema-payload status database-schema-status connection]}]]
   (let [schema {:status status
                 :data (assoc (-> db :database-schema :data)
                              (:connection-name connection)
                              {:status status
                               :database-schema-status database-schema-status
                               :type (:type connection)
                               :raw schema-payload
                               :schema-tree (process-schema schema-payload)
                               :indexes-tree (process-indexes schema-payload)})
                 :type (:type connection)
                 :raw schema-payload
                 :schema-tree (process-schema schema-payload)
                 :indexes-tree (process-indexes schema-payload)}]
     {:db (assoc-in db [:database-schema] schema)})))

;; Event unified to handle schema for all databases
(rf/reg-event-fx
 :editor-plugin->change-database
 (fn [{:keys [db]} [_ connection database]]
   (.setItem js/localStorage "selected-database" database)
   {:fx [[:dispatch [:editor-plugin->get-multi-database-schema
                     connection
                     database
                     (get-in db [:database-schema :data (:connection-name connection) :databases])]]]}))
