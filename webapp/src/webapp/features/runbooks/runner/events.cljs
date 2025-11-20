(ns webapp.features.runbooks.runner.events
  (:require
   [clojure.string :as cs]
   [re-frame.core :as rf]))

(rf/reg-event-db
 :runbooks/set-active-runbook
 (fn
   [db [_ template repository]]
   (assoc db :runbooks-plugin->selected-runbooks {:status :ready
                                                  :data {:name (:name template)
                                                         :error (:error template)
                                                         :params (keys (:metadata template))
                                                         :file_url (:file_url template)
                                                         :metadata (:metadata template)
                                                         :connections (:connections template)
                                                         :repository (:repository repository) :ref-hash (:commit repository)}})))

(rf/reg-event-db
 :runbooks/set-active-runbook-by-name
 (fn
   [db [_ runbook-name]]
   (let [list-data (get-in db [:runbooks :list])
         repositories (:data list-data)
         all-items (mapcat :items (or repositories []))
         runbook  (some (fn [r] (when (= (:name r) runbook-name) r)) all-items)]
     (if runbook
       (assoc db :runbooks-plugin->selected-runbooks
              {:status :ready
               :data {:name        (:name runbook)
                      :error       (:error runbook)
                      :params      (keys (:metadata runbook))
                      :file_url    (:file_url runbook)
                      :metadata    (:metadata runbook)
                      :connections (:connections runbook)}})
       (assoc db :runbooks-plugin->selected-runbooks {:status :error :data nil})))))

;; Connection Events
(rf/reg-event-fx
 :runbooks/set-selected-connection
 (fn [{:keys [db]} [_ connection]]
   {:db (assoc-in db [:runbooks :selected-connection] connection)
    :fx [[:dispatch [:runbooks/persist-selected-connection]]
         [:dispatch [:runbooks-plugin->clear-active-runbooks]]
         [:dispatch [:runbooks/update-runbooks-for-connection]]]}))

(rf/reg-event-fx
 :runbooks/persist-selected-connection
 (fn [{:keys [db]} _]
   (let [selected (get-in db [:runbooks :selected-connection])]
     (.setItem js/localStorage
               "runbooks-selected-connection"
               (:name selected))
     {})))

(rf/reg-event-fx
 :runbooks/load-persisted-connection
 (fn [_ _]
   (let [saved (.getItem js/localStorage "runbooks-selected-connection")]
     ;; If old format (starts with "{"), clear it and start fresh
     (if (and saved (.startsWith saved "{"))
       (do
         (.removeItem js/localStorage "runbooks-selected-connection")
         {})
       ;; New format - fetch the connection
       (if (and saved (not= saved "null") (not= saved ""))
         {:fx [[:dispatch [:connections->get-connection-details
                           saved
                           [:runbooks/connection-loaded]]]]}
         {})))))

(rf/reg-event-fx
 :runbooks/connection-loaded
 (fn [{:keys [db]} [_ connection-name]]
   (let [connection (get-in db [:connections :details connection-name])]
     (if connection
       {:db (assoc-in db [:runbooks :selected-connection] connection)
        :fx [[:dispatch [:runbooks/update-runbooks-for-connection]]]}
       ;; Connection not found - clear selection and reload list without connection
       {:db (assoc-in db [:runbooks :selected-connection] nil)
        :fx [[:dispatch [:runbooks/persist-selected-connection]]
             [:dispatch [:runbooks/list nil]]]}))))

(rf/reg-event-fx
 :runbooks/update-runbooks-for-connection
 (fn [{:keys [db]} _]
   (let [selected-connection (get-in db [:runbooks :selected-connection])
         connection-name (when selected-connection (:name selected-connection))]
     {:fx [[:dispatch [:runbooks/list connection-name]]]})))

;; Dialog Events
(rf/reg-event-db
 :runbooks/toggle-connection-dialog
 (fn [db [_ open?]]
   (assoc-in db [:runbooks :connection-dialog-open?] open?)))

;; Execution Events
(rf/reg-event-db
 :runbooks/trigger-execute
 (fn [db _]
   (assoc-in db [:runbooks :execute-trigger] true)))

(rf/reg-event-db
 :runbooks/execute-handled
 (fn [db _]
   (assoc-in db [:runbooks :execute-trigger] false)))

(defn metadata->json-stringify
  [metadata]
  (->> metadata
       (filter (fn [{:keys [key value]}]
                 (not (or (cs/blank? key) (cs/blank? value)))))
       (map (fn [{:keys [key value]}] {key value}))
       (reduce into {})
       (clj->js)))

(rf/reg-event-fx
 :runbooks/exec
 (fn
   [{:keys [db]} [_ {:keys [file-name params connection-name repository ref-hash jira_fields cmdb_fields client-args]}]]
   (let [selected-connection (get-in db [:runbooks :selected-connection])
         selected-db (.getItem js/localStorage "selected-database")
         is-dynamodb? (= (:subtype selected-connection) "dynamodb")
         is-cloudwatch? (= (:subtype selected-connection) "cloudwatch")
         keep-metadata? (get-in db [:runbooks :keep-metadata?])
         current-metadatas (get-in db [:runbooks :metadata])
         current-metadata-key (get-in db [:runbooks :metadata-key])
         current-metadata-value (get-in db [:runbooks :metadata-value])
         metadata (conj current-metadatas {:key current-metadata-key :value current-metadata-value})
         repository (or repository
                        (let [list-data (get-in db [:runbooks :list])
                              repositories (or (:data list-data) [])
                              repo (first (filter #(some (fn [item] (= (:name item) file-name)) (:items %)) repositories))]
                          (when repo (:repository repo))))
         env-vars (cond
                    (and is-dynamodb? selected-db)
                    {"envvar:TABLE_NAME" (js/btoa selected-db)}

                    (and is-cloudwatch? selected-db)
                    {"envvar:LOG_GROUP_NAME" (js/btoa selected-db)}

                    :else nil)
         payload (cond-> {:file_name file-name
                          :connection_name connection-name
                          :repository repository
                          :parameters params
                          :env_vars env-vars
                          :metadata (metadata->json-stringify metadata)
                          :client_args (or client-args [])}
                   ref-hash (assoc :ref_hash ref-hash)
                   jira_fields (assoc :jira_fields jira_fields)
                   cmdb_fields (assoc :cmdb_fields cmdb_fields))
         on-failure (fn [_error-message error]
                      (rf/dispatch [:show-snackbar {:text "Failed to execute runbook"
                                                    :level :error
                                                    :details _error-message}])
                      (rf/dispatch [:runbooks/exec-failure error]))
         on-success (fn [res]
                      (rf/dispatch
                       [:show-snackbar {:level :success
                                        :text "Runbook was executed!"}])
                      (rf/dispatch [:runbooks/exec-success res file-name])
                      (rf/dispatch [:webapp.events.editor-plugin/editor-plugin->set-script-success res file-name]))
         base-db (assoc db :runbooks->exec {:status :loading :data nil})]
     (merge {:db base-db
             :fx [[:dispatch [:fetch {:method "POST"
                                      :uri "/runbooks/exec"
                                      :on-success on-success
                                      :on-failure on-failure
                                      :body payload}]]]}
            (when-not keep-metadata?
              {:db (-> base-db
                       (assoc-in [:runbooks :metadata] [])
                       (assoc-in [:runbooks :metadata-key] "")
                       (assoc-in [:runbooks :metadata-value] ""))})))))

(rf/reg-event-fx
 :runbooks/exec-success
 (fn [{:keys [db]} [_ data file-name]]
   {:db (assoc-in db [:runbooks->exec] {:status :success
                                        :data (merge data {:file-name file-name})})}))

(rf/reg-event-fx
 :runbooks/exec-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:runbooks->exec] {:status :failure :data error})}))

;; Execution Subscriptions
(rf/reg-sub
 :runbooks->exec
 (fn [db]
   (get-in db [:runbooks->exec] {:status :idle :data nil})))

;; Metadata Events
(rf/reg-event-db
 :runbooks/add-metadata
 (fn [db [_ metadata]]
   (let [old-metadata (or (get-in db [:runbooks :metadata]) [])]
     (update db :runbooks merge {:metadata (conj old-metadata metadata)
                                 :metadata-key ""
                                 :metadata-value ""}))))

(rf/reg-event-db
 :runbooks/update-metadata-key
 (fn [db [_ value]]
   (assoc-in db [:runbooks :metadata-key] value)))

(rf/reg-event-db
 :runbooks/update-metadata-value
 (fn [db [_ value]]
   (assoc-in db [:runbooks :metadata-value] value)))

(rf/reg-event-db
 :runbooks/update-metadata-at-index
 (fn [db [_ index field value]]
   (assoc-in db [:runbooks :metadata index field] value)))

;; Metadata Subscriptions
(rf/reg-sub
 :runbooks/metadata
 (fn [db]
   (get-in db [:runbooks :metadata] [])))

(rf/reg-sub
 :runbooks/metadata-key
 (fn [db]
   (get-in db [:runbooks :metadata-key] "")))

(rf/reg-sub
 :runbooks/metadata-value
 (fn [db]
   (get-in db [:runbooks :metadata-value] "")))

(rf/reg-event-db
 :runbooks/toggle-keep-metadata
 (fn [db [_]]
   (update-in db [:runbooks :keep-metadata?] not)))

(rf/reg-sub
 :runbooks/keep-metadata?
 (fn [db]
   (get-in db [:runbooks :keep-metadata?] false)))
