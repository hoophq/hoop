(ns webapp.events.audit
  (:require
   [clojure.string :as string]
   [re-frame.core :as rf]
   [webapp.formatters :as formatters]
   [webapp.jira-templates.loading-jira-templates :as loading-jira-templates]))

(rf/reg-event-fx
 :audit->get-sessions
 (fn
   [{:keys [db]} [_ limit params]]
   (let [search (.. js/window -location -search)
         url-search-params (new js/URLSearchParams search)
         url-params-list (js->clj (for [q url-search-params] q))
         url-params-map (into (sorted-map) url-params-list)
         query-params (merge url-params-map {"limit" (or limit 20)} params)]
     {:fx [[:dispatch [:fetch {:method "GET"
                               :uri "/plugins/audit/sessions"
                               :query-params query-params
                               :on-success #(rf/dispatch [:audit->set-sessions %])}]]
           [:dispatch [:audit->set-audit-status :loading]]]
      :db (assoc-in db [:audit :filters] url-params-map)})))

(rf/reg-event-fx
 :audit->set-sessions
 (fn
   [{:keys [db]} [_ sessions]]
   {:fx [[:dispatch [:audit->set-audit-status :success]]]
    :db (assoc-in db [:audit :sessions] sessions)}))

(rf/reg-event-fx
 :audit->get-filtered-sessions-by-id
 (fn
   [{:keys [db]} [_ session-id-list]]
   (let [on-failure (fn [error]
                      (rf/dispatch [::audit->set-filtered-sessions-by-id nil session-id-list error]))
         on-success (fn [res]
                      (rf/dispatch [::audit->set-filtered-sessions-by-id res session-id-list nil]))
         dispatchs (mapv (fn [session-id]
                           [:dispatch-later
                            {:ms 1000
                             :dispatch [:fetch {:method "GET"
                                                :uri (str "/sessions/" session-id "?event_stream=base64")
                                                :on-success on-success
                                                :on-failure on-failure}]}])
                         session-id-list)]
     {:db (assoc db :audit->filtered-session-by-id {:data [] :status :loading :errors []})
      :fx dispatchs})))

(rf/reg-event-fx
 ::audit->set-filtered-sessions-by-id
 (fn
   [{:keys [db]} [_ session session-id-list error]]
   (let [current-state (:audit->filtered-session-by-id db)
         current-data (:data current-state)
         current-errors (:errors current-state)
         ;; Only add session if it's not an error (has an :id field and no :status error field)
         new-data (if (and session (not error) (:id session))
                    (concat [session] current-data)
                    current-data)
         ;; Track errors (404s)
         new-errors (if error
                      (conj (or current-errors []) error)
                      current-errors)
         ;; Count successful sessions and errors to determine if we're done
         total-processed (+ (count new-data) (count new-errors))
         finished? (>= total-processed (count session-id-list))]
     {:db (assoc db :audit->filtered-session-by-id {:data new-data
                                                    :errors new-errors
                                                    :status (if finished?
                                                              :ready
                                                              :loading)})})))

(rf/reg-event-db
 :audit->clear-filtered-sessions-by-id
 (fn
   [db [_]]
   (assoc db :audit->filtered-session-by-id {:data [] :status :idle :errors []})))

(rf/reg-event-fx
 :audit->get-sessions-by-batch-id
 (fn
   [{:keys [db]} [_ batch-id]]
   (let [on-failure (fn [error]
                      (rf/dispatch [::audit->set-sessions-by-batch-id nil error true]))
         on-success (fn [res]
                      (rf/dispatch [::audit->set-sessions-by-batch-id res nil true]))]
     {:db (assoc db :audit->filtered-session-by-id {:data [] :status :loading :errors [] :offset 0 :has-more? false :loading true :search-term ""})
      :fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/sessions?batch_id=" batch-id "&limit=20&offset=0")
                               :on-success on-success
                               :on-failure on-failure}]]]})))

(rf/reg-event-fx
 :audit->get-sessions-by-batch-id-next-page
 (fn
   [{:keys [db]} [_ batch-id]]
   (let [current-state (:audit->filtered-session-by-id db)
         data-count (count (:data current-state []))
         next-offset data-count
         on-failure (fn [error]
                      (rf/dispatch [::audit->set-sessions-by-batch-id nil error false]))
         on-success (fn [res]
                      (rf/dispatch [::audit->set-sessions-by-batch-id res nil false]))]
     {:db (assoc-in db [:audit->filtered-session-by-id :loading] true)
      :fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/sessions?batch_id=" batch-id "&limit=20&offset=" next-offset)
                               :on-success on-success
                               :on-failure on-failure}]]]})))

(rf/reg-event-db
 ::audit->set-sessions-by-batch-id
 (fn
   [db [_ response error force-refresh?]]
   (let [session-data (or (:data response) [])
         has-next-page (or (:has_next_page response) false)
         current-state (:audit->filtered-session-by-id db)
         current-search-term (:search-term current-state "")
         existing-data (:data current-state [])
         final-data (if force-refresh?
                      session-data
                      (vec (concat existing-data session-data)))
         new-offset (if force-refresh?
                      (count session-data)
                      (+ (:offset current-state 0) (count session-data)))]
     (assoc db :audit->filtered-session-by-id {:data final-data
                                               :errors (if error [error] [])
                                               :status (if error :error :ready)
                                               :search-term current-search-term
                                               :offset new-offset
                                               :has-more? has-next-page
                                               :loading false}))))

(rf/reg-event-db
 :audit->set-filtered-session-search
 (fn [db [_ term]]
   (assoc-in db [:audit->filtered-session-by-id :search-term] term)))

(rf/reg-event-fx
 :audit->set-audit-status
 (fn
   [{:keys [db]} [_ status]]
   {:db (assoc-in db [:audit :status] status)}))

(rf/reg-event-fx
 :audit->filter-sessions
 (fn
   [{:keys [db]} [_ hashmap]]
   (let [status-param (into {} (remove (comp string/blank? second) hashmap))
         filters (apply dissoc (:filters (:audit db)) (keys hashmap))
         query-params (merge status-param filters)]
     {:fx [[:dispatch [:navigate :sessions query-params]]
           [:dispatch [:audit->get-sessions]]]})))

(rf/reg-event-fx
 :audit->get-session-details-page
 (fn
   [{:keys [db]} [_ session-id]]
   (let [state {:status :loading
                :session nil
                :session-logs {:status :loading}}]
     {:db (assoc db :audit->session-details state)
      :fx [[:dispatch [:fetch
                       {:method "GET"
                        :uri (str "/sessions/" session-id)
                        :on-success #(rf/dispatch [:audit->get-session-by-id {:id (:id %) :verb (:verb %)}])}]]]})))

(rf/reg-event-fx
 :audit->get-session-by-id
 (fn
   [{:keys [db]} [_ session]]
   (let [state {:status :loading
                :session session
                :session-logs {:status :loading}}
         event-stream (if (= "exec" (:verb session))
                        "?event_stream=base64"
                        "")]
     {:db (assoc db :audit->session-details state)
      :fx [[:dispatch [:fetch
                       {:method "GET"
                        :uri (str "/sessions/" (:id session) event-stream)
                        :on-success #(rf/dispatch [:audit->check-session-size %])}]]]})))

(def size-threshold (* 4 1024 1024)) ; 4MB threshold

(rf/reg-event-fx
 :audit->check-session-size
 (fn
   [{:keys [db]} [_ session]]
   (let [event-size (:event_size session)
         script-size (:script_size session)
         has-large-event? (and event-size (> event-size size-threshold))
         has-large-input? (and script-size (> script-size size-threshold))
         event-stream (if (= "exec" (:verb session))
                        "event_stream=base64"
                        (if (= "postgres" (:connection_subtype session))
                          "event_stream=raw-queries"
                          ""))
         expand-parts (cond-> []
                        (not has-large-event?) (conj "event_stream")
                        (not has-large-input?) (conj "session_input"))
         expand-param (string/join "," expand-parts)
         query-parts (cond-> []
                       (seq expand-param) (conj (str "expand=" expand-param))
                       (and (not has-large-event?) (seq event-stream)) (conj event-stream))
         query-string (when (seq query-parts)
                        (str "?" (string/join "&" query-parts)))
         base-state {:session session
                     :status :success
                     :has-large-payload? has-large-event?
                     :has-large-input? has-large-input?}]
     (if (and has-large-event? has-large-input?)
       {:db (assoc db :audit->session-details base-state)}
       {:db (assoc db :audit->session-details (assoc base-state :status :loading))
        :fx [[:dispatch [:fetch
                         {:method "GET"
                          :uri (str "/sessions/" (:id session) query-string)
                          :on-success (fn [session-data]
                                        (rf/dispatch [:audit->set-session session-data])
                                        (rf/dispatch [:reports->get-report-by-session-id session-data]))}]]]}))))

(rf/reg-event-db
 :audit->clear-session-details-state
 (fn
   [db [_ {:keys [status]}]]
   (let [state {:status status
                :session nil
                :session-logs {:status status}}]
     (assoc db :audit->session-details state))))

(rf/reg-event-fx
 :audit->set-session
 (fn
   [{:keys [db]} [_ details]]
   (let [cached-session (-> db :audit->session-details :session)
         updated-session (merge cached-session details)
         session-details-state (:audit->session-details db)
         has-large-payload? (:has-large-payload? session-details-state)
         has-large-input? (:has-large-input? session-details-state)]
     {:db (assoc db
                 :audit->session-details
                 {:session updated-session
                  :status :success
                  :has-large-payload? has-large-payload?
                  :has-large-input? has-large-input?
                  :session-logs (:session-logs session-details-state)})})))

(rf/reg-event-fx
 :audit->clear-session
 (fn
   [{:keys [db]} [_]]
   {:db (-> db
            (assoc :audit->session-details {:status :loading
                                            :session nil
                                            :session-logs {:status :loading}})
            (assoc :audit->session-logs {:status :idle :data nil}))}))

(rf/reg-event-fx
 :audit->get-next-sessions-page
 (fn
   [{:keys [db]} [_ _]]
   (let [sessions (-> db :audit :sessions :data)]
     {:fx [[:dispatch [:audit->get-sessions (+ (count sessions) 20)]]]})))

(rf/reg-event-fx
 :audit->execute-session
 (fn
   [{:keys [db]} [_ session]]
   (let [success (fn [res]
                   (js/setTimeout
                    (fn []
                      (rf/dispatch [:audit->get-sessions])
                      (rf/dispatch [:audit->get-session-by-id {:id (:session_id res) :verb "exec"}]))
                    800))
         failure (fn [error res]
                   (let [session-id (:session_id res)]
                     (if (and session-id (> (count session-id) 0))
                       (success res)
                       (rf/dispatch [:show-snackbar {:text "Failed to execute script"
                                                     :level :error
                                                     :details error}]))))]
     {:fx [[:dispatch [:fetch (merge
                               {:method "POST"
                                :uri (str "/sessions/" (:id session) "/exec")
                                :on-success success
                                :on-failure failure})]]]})))

(rf/reg-event-fx
 :audit->re-run-session
 (fn [{:keys [db]} [_ session]]
   ;; First fetch the connection details
   {:db (assoc-in db [:audit->rerun :session] session)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/connections/" (:connection session))
                             :on-success #(rf/dispatch [:audit->handle-rerun-with-connection % session])
                             :on-failure (fn [error]
                                           (rf/dispatch [:show-snackbar
                                                         {:text "Failed to fetch connection details"
                                                          :level :error}]))}]]]}))

(rf/reg-event-fx
 :audit->handle-rerun-with-connection
 (fn [{:keys [db]} [_ connection session]]
   (let [payload {:script (-> session :script :data)
                  :labels {:re-run-from (:id session)}
                  :connection (:connection session)}

         jira-integration-enabled? (= "enabled" (-> db :jira-integration->details :data :status))
         needs-template? (boolean (and connection
                                       (not (string/blank? (:jira_issue_template_id connection)))
                                       jira-integration-enabled?))

         handle-submit (fn [form-data]
                         (let [exec-data (cond-> payload
                                           (:jira_fields form-data)
                                           (assoc :jira_fields (:jira_fields form-data))

                                           (:cmdb_fields form-data)
                                           (assoc :cmdb_fields (:cmdb_fields form-data)))]
                           (rf/dispatch [:fetch
                                         {:method "POST"
                                          :uri "/sessions"
                                          :body exec-data
                                          :on-success (fn [res]
                                                        (js/setTimeout
                                                         (fn []
                                                           (rf/dispatch
                                                            [:show-snackbar
                                                             {:level :success
                                                              :text "The session was re-ran"}])
                                                           (rf/dispatch [:audit->get-sessions])
                                                           (rf/dispatch
                                                            [:audit->get-session-by-id
                                                             {:id (:session_id res)
                                                              :verb "exec"}]))
                                                         800))
                                          :on-failure (fn [error res]
                                                        (let [session-id (:session_id res)]
                                                          (if (and session-id
                                                                   (> (count session-id) 0))
                                                            (js/setTimeout
                                                             (fn []
                                                               (rf/dispatch
                                                                [:show-snackbar
                                                                 {:level :success
                                                                  :text "The session was re-ran"}])
                                                               (rf/dispatch [:audit->get-sessions])
                                                               (rf/dispatch
                                                                [:audit->get-session-by-id
                                                                 {:id session-id
                                                                  :verb "exec"}]))
                                                             800)
                                                            (rf/dispatch
                                                             [:show-snackbar
                                                              {:text error
                                                               :level :error}]))))}])
                           (rf/dispatch [:modal->close])))]

     (if needs-template?
       ;; Handle JIRA template flow
       {:fx [[:dispatch [:modal->open
                         {:maxWidth "540px"
                          :custom-on-click-out (fn [event]
                                                 (.preventDefault event))
                          :content [loading-jira-templates/main]}]]
             [:dispatch [:jira-templates->get-submit-template-re-run
                         (:jira_issue_template_id connection)]]]
        :db (assoc db :on-template-verified handle-submit)}

       ;; Original flow without JIRA
       {:fx [[:dispatch [:fetch
                         {:method "POST"
                          :uri "/sessions"
                          :body payload
                          :on-success (fn [res]
                                        (js/setTimeout
                                         (fn []
                                           (rf/dispatch
                                            [:show-snackbar
                                             {:level :success
                                              :text "The session was re-ran"}])
                                           (rf/dispatch [:audit->get-sessions])
                                           (rf/dispatch
                                            [:audit->get-session-by-id
                                             {:id (:session_id res)
                                              :verb "exec"}]))
                                         800))
                          :on-failure (fn [error res]
                                        (let [session-id (:session_id res)]
                                          (if (and session-id
                                                   (> (count session-id) 0))
                                            (handle-submit nil)
                                            (rf/dispatch
                                             [:show-snackbar
                                              {:text error
                                               :level :error}]))))}]]]}))))

(rf/reg-event-fx
 :audit->add-review
 (fn
   [_ [_ session status & {:keys [start-time
                                  end-time
                                  force-review]}]]
   (let [body (cond-> {:status (string/upper-case status)}
                (and start-time end-time)
                (assoc :time_window {:type "time_range"
                                     :configuration {:start_time (formatters/local-time->utc-time start-time)
                                                     :end_time (formatters/local-time->utc-time end-time)}})

                force-review
                (assoc :force_review true))]
     {:fx [[:dispatch
            [:fetch {:method "PUT"
                     :uri (str "/reviews/" (-> session :review :id))
                     :body body
                     :on-success
                     (fn []
                       (rf/dispatch [:show-snackbar
                                     {:level :success
                                      :text "Your review was added"}])
                       (js/setTimeout
                        (fn []
                          (rf/dispatch [:audit->get-sessions])
                          (rf/dispatch [:audit->get-session-by-id session]))
                        500))
                     :on-failure #(rf/dispatch [:show-snackbar {:text "Failed to add review"
                                                                :level :error
                                                                :details %}])}]]]})))

(rf/reg-event-fx
 :audit->session-file-generate
 (fn
   [{:keys [db]} [_ session-id extension]]
   (let [success (fn [res] (.open js/window (:download_url res)))
         failure (fn [error]
                   (rf/dispatch [:show-snackbar {:text "Failed to generate session file"
                                                 :level :error
                                                 :details error}]))]
     {:fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/sessions/"
                                         session-id
                                         "?extension="
                                         extension)
                               :on-success success
                               :on-failure failure}]]]})))

(rf/reg-event-fx
 :audit->session-input-download
 (fn
   [{:keys [_]} [_ session-id]]
   (let [success (fn [res] (.open js/window (:download_url res)))
         failure (fn [error]
                   (rf/dispatch [:show-snackbar {:text "Failed to download session input"
                                                 :level :error
                                                 :details error}]))]
     {:fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/sessions/"
                                         session-id
                                         "?blob-type=session_input&extension=txt")
                               :on-success success
                               :on-failure failure}]]]})))

(rf/reg-event-fx
 :audit->connect-session
 (fn
   [{:keys [db]} [_ session connecting-status]]
   (let [connection-name (:connection session)
         revoke-at (when (get-in session [:review :revoke_at])
                     (js/Date. (get-in session [:review :revoke_at])))
         not-revoked? (when revoke-at (> (.getTime revoke-at) (.getTime (js/Date.))))
         access-duration (get-in session [:review :access_duration])]

     (if not-revoked?
       ;; If not revoked, connect
       {:fx [[:dispatch [:connections->start-connect-with-settings
                         {:connection-name connection-name
                          :port "8999"
                          :access-duration access-duration}
                         connecting-status]]]}

       ;; If revoked, show error message
       {:fx [[:dispatch [:show-snackbar {:level :error
                                         :text "This connection approval has expired."}]]
             [:dispatch [:reset-connecting-status connecting-status]]]}))))

(rf/reg-event-fx
 :audit->kill-session
 (fn
   [{:keys [db]} [_ session killing-status]]
   {:fx [[:dispatch [:fetch {:method "POST"
                             :uri (str "/sessions/" (:id session) "/kill")
                             :on-success (fn [_]
                                           (when killing-status
                                             (reset! killing-status :ready))
                                           (rf/dispatch [:show-snackbar
                                                         {:level :success
                                                          :text "Session killed successfully"}])
                                           (rf/dispatch [:audit->get-session-by-id session]))
                             :on-failure (fn [error]
                                           (when killing-status
                                             (reset! killing-status :ready))
                                           (rf/dispatch [:show-snackbar
                                                         {:level :error
                                                          :text "Failed to kill session"
                                                          :details error}]))}]]]}))

(rf/reg-fx
 :reset-connecting-status
 (fn [connecting-status]
   (when (and connecting-status (satisfies? IAtom connecting-status))
     (reset! connecting-status :ready))))

(rf/reg-event-fx
 :audit->get-session-logs-data
 (fn
   [{:keys [db]} [_ session-id]]
   {:db (assoc-in db [:audit->session-logs] {:status :loading :data nil})
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri (str "/sessions/" session-id "?expand=event_stream,session_input&event_stream=base64")
                      :on-success #(rf/dispatch [:audit->set-session-logs-data %])
                      :on-failure (fn [error]
                                    (rf/dispatch [:show-snackbar
                                                  {:text "Failed to load session logs"
                                                   :level :error
                                                   :details error}])
                                    (rf/dispatch [:audit->set-session-logs-error]))}]]]}))

(rf/reg-event-db
 :audit->set-session-logs-data
 (fn
   [db [_ session-data]]
   (assoc-in db [:audit->session-logs] {:status :success
                                        :data (:event_stream session-data)})))

(rf/reg-event-db
 :audit->set-session-logs-error
 (fn
   [db [_]]
   (assoc-in db [:audit->session-logs] {:status :error :data nil})))
