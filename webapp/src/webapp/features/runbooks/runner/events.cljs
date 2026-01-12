(ns webapp.features.runbooks.runner.events
  (:require
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.jira-templates.loading-jira-templates :as loading-jira-templates]
   [webapp.jira-templates.prompt-form :as prompt-form]))

(rf/reg-event-db
 :runbooks/set-active-runbook
 (fn
   [db [_ template repository]] 
   (let [repository-str (if (string? repository) repository (:repository repository))
         list-data (get-in db [:runbooks :list])
         repositories (or (:data list-data) [])
         ;; If repository is a string, look up the repository object from list data
         repository-obj (if (string? repository)
                          (first (filter #(= (:repository %) repository-str) repositories))
                          repository)
         ;; If template only has :name (minimal from search), look up full runbook data
         full-template (if (and (:name template) (nil? (:metadata template)))
                         (let [repo-items (if repository-obj
                                            (:items repository-obj)
                                            (mapcat :items repositories))
                               runbook (some (fn [r] (when (= (:name r) (:name template)) r)) repo-items)]
                           (or runbook template))
                         template)
         ref-hash (when repository-obj (:commit repository-obj))
         metadata (or (:metadata full-template) {})]
     (assoc db :runbooks->selected-runbooks {:status :ready
                                             :data {:name (:name full-template)
                                                    :error (:error full-template)
                                                    :params (keys metadata)
                                                    :file_url (:file_url full-template)
                                                    :metadata metadata
                                                    :connections (:connections full-template)
                                                    :repository repository-str
                                                    :ref-hash ref-hash}}))))

;; Connection Events
(rf/reg-event-fx
 :runbooks/set-selected-connection
 (fn [{:keys [db]} [_ connection]]
   {:db (assoc-in db [:runbooks :selected-connection] connection)
    :fx [[:dispatch [:runbooks/persist-selected-connection]]
         [:dispatch [:runbooks/clear-active-runbooks]]
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
 :runbooks/clear-persisted-connection
 (fn [_ _]
   (.removeItem js/localStorage "runbooks-selected-connection")
   {}))

(rf/reg-event-fx
 :runbooks/load-persisted-connection
 (fn [_ _]
   (let [saved (.getItem js/localStorage "runbooks-selected-connection")]
     ;; If old format (starts with "{"), clear it and start fresh
     (if (and saved (.startsWith saved "{"))
       (do
         (.removeItem js/localStorage "runbooks-selected-connection")
         {:fx [[:dispatch [:runbooks/list nil]]]})
       ;; New format - fetch the connection
       (if (and saved (not= saved "null") (not= saved ""))
         {:fx [[:dispatch [:connections->get-connection-details
                           saved
                           [:runbooks/connection-loaded]]]]}
         {:fx [[:dispatch [:runbooks/list nil]]]})))))

(rf/reg-event-fx
 :runbooks/connection-loaded
 (fn [{:keys [db]} [_ connection-name]]
   (let [connection (get-in db [:connections :details connection-name])
         enabled? (and connection
                       (not= "disabled" (:access_mode_runbooks connection)))]
     (if enabled?
       {:db (assoc-in db [:runbooks :selected-connection] connection)
        :fx [[:dispatch [:runbooks/update-runbooks-for-connection]]]}
       {:db (assoc-in db [:runbooks :selected-connection] nil)
        :fx [[:dispatch [:runbooks/clear-persisted-connection]]
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
   [{:keys [db]} [_ {:keys [file-name params connection-name repository ref-hash jira_fields cmdb_fields on-success on-failure] :as context}]]
   ;; Check if parallel mode is active
   (let [parallel-connections (get-in db [:parallel-mode :selection :connections])
         parallel-mode? (>= (count parallel-connections) 2)]
     
     (if parallel-mode?
       ;; Use parallel mode execution
       (do
         (js/console.log "✅ Using PARALLEL MODE execution for runbooks")
         {:fx [[:dispatch [:connections->get-multiple-by-names
                           (map :name parallel-connections)
                           [:parallel-mode/submit-runbook-with-fresh-data context]
                           [:runbooks/submit-task-connection-error]]]]})
       
       ;; Use single runbook execution (existing flow)
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
                          :metadata (metadata->json-stringify metadata)}
                   ref-hash (assoc :ref_hash ref-hash)
                   jira_fields (assoc :jira_fields jira_fields)
                   cmdb_fields (assoc :cmdb_fields cmdb_fields))
         default-on-failure (fn [_error-message error]
                              (rf/dispatch [:show-snackbar {:text "Failed to execute runbook"
                                                            :level :error
                                                            :details _error-message}])
                              (rf/dispatch [:runbooks/exec-failure error])
                              (when on-failure
                                (on-failure _error-message error)))
         default-on-success (fn [res]
                              (rf/dispatch
                               [:show-snackbar {:level :success
                                                :text "Runbook was executed!"}])
                              (rf/dispatch [:runbooks/exec-success res file-name])
                              (rf/dispatch [:webapp.events.editor-plugin/editor-plugin->set-script-success res file-name])
                              (when on-success
                                (on-success res)))
         base-db (assoc db :runbooks->exec {:status :loading :data nil})]
     (merge {:db base-db
             :fx [[:dispatch [:fetch {:method "POST"
                                      :uri "/runbooks/exec"
                                      :on-success default-on-success
                                      :on-failure default-on-failure
                                      :body payload}]]]}
            (when-not keep-metadata?
              {:db (-> base-db
                       (assoc-in [:runbooks :metadata] [])
                       (assoc-in [:runbooks :metadata-key] "")
                       (assoc-in [:runbooks :metadata-value] ""))})))))))

(rf/reg-event-fx
 :runbooks/submit-task-connection-error
 (fn [_ [_ _error]]
   {:fx [[:dispatch [:show-snackbar
                     {:level :error
                      :text "Failed to verify connections. Please try again."}]]]}))

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

;; Selected Runbook Events
(rf/reg-event-db
 :runbooks/clear-active-runbooks
 (fn [db _]
   (assoc db :runbooks->selected-runbooks {:status :idle :data nil})))

;; Filtered Runbooks Events
(rf/reg-event-db
 :runbooks/set-filtered-runbooks
 (fn [db [_ runbooks]]
   (assoc db :runbooks->filtered-runbooks runbooks)))

;; Selected Runbook Subscriptions
(rf/reg-sub
 :runbooks->selected-runbooks
 (fn [db]
   (get-in db [:runbooks->selected-runbooks] {:status :idle :data nil})))

;; Filtered Runbooks Subscriptions
(rf/reg-sub
 :runbooks->filtered-runbooks
 (fn [db]
   (get-in db [:runbooks->filtered-runbooks] [])))

;; Runbooks Jira Template Events

(rf/reg-event-fx
 :runbooks/show-jira-form
 (fn [_ [_ {:keys [template-id file-name params connection-name repository ref-hash]}]]
   {:fx [[:dispatch [:modal->open
                     {:maxWidth "540px"
                      :custom-on-click-out #(.preventDefault %)
                      :content [loading-jira-templates/main]}]]
         [:dispatch [:jira-templates->get-submit-template template-id]]
         [:dispatch-later
          {:ms 1000
           :dispatch [:runbooks/check-jira-template-and-show-form
                      {:template-id template-id
                       :file-name file-name
                       :params params
                       :connection-name connection-name
                       :repository repository
                       :ref-hash ref-hash}]}]]}))

(defn- needs-form? [template]
  (let [has-prompts? (seq (get-in template [:data :prompt_types :items]))
        has-cmdb? (when-let [cmdb-items (get-in template [:data :cmdb_types :items])]
                    (some (fn [{:keys [value jira_values]}]
                            (when (and value jira_values)
                              (not-any? #(= value (:name %)) jira_values)))
                          cmdb-items))]
    (or has-prompts? has-cmdb?)))

(rf/reg-event-fx
 :runbooks/check-jira-template-and-show-form
 (fn [{:keys [db]} [_ {:keys [template-id file-name params connection-name repository ref-hash] :as context}]]
   (let [template (get-in db [:jira-templates->submit-template])]
     (cond
       ;; Still loading
       (or (nil? (:data template))
           (= :loading (:status template)))
       {:fx [[:dispatch-later
              {:ms 500
               :dispatch [:runbooks/check-jira-template-and-show-form
                          {:template-id template-id
                           :file-name file-name
                           :params params
                           :connection-name connection-name
                           :repository repository
                           :ref-hash ref-hash}]}]]}

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
                                                  [:runbooks/handle-jira-template-submit
                                                   {:form-data %
                                                    :file-name file-name
                                                    :params params
                                                    :connection-name connection-name
                                                    :repository repository
                                                    :ref-hash ref-hash}])}]}]]]}

       ;; Ready and doesn't need form
       :else
       {:fx [[:dispatch [:runbooks/handle-jira-template-submit
                         {:form-data nil
                          :file-name file-name
                          :params params
                          :connection-name connection-name
                          :repository repository
                          :ref-hash ref-hash}]]]}))))

(rf/reg-event-fx
 :runbooks/handle-jira-template-submit
 (fn [_ [_ {:keys [form-data file-name params connection-name repository ref-hash]}]]
   {:fx [[:dispatch [:modal->close]]
         [:dispatch [:runbooks/exec
                     (cond-> {:file-name file-name
                              :params params
                              :connection-name connection-name
                              :repository repository
                              :ref-hash ref-hash}
                       (:jira_fields form-data) (assoc :jira_fields (:jira_fields form-data))
                       (:cmdb_fields form-data) (assoc :cmdb_fields (:cmdb_fields form-data)))]]]}))
