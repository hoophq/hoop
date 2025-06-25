(ns webapp.events.audit
  (:require
   [clojure.string :as string]
   [re-frame.core :as rf]
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
                      (rf/dispatch [::audit->set-filtered-sessions-by-id error session-id-list]))
         on-success (fn [res]
                      (rf/dispatch [::audit->set-filtered-sessions-by-id res session-id-list]))
         dispatchs (mapv (fn [session-id]
                           [:dispatch-later
                            {:ms 1000
                             :dispatch [:fetch {:method "GET"
                                                :uri (str "/sessions/" session-id "?event_stream=base64")
                                                :on-success on-success
                                                :on-failure on-failure}]}])
                         session-id-list)]
     {:db (assoc db :audit->filtered-session-by-id {:data [] :status :loading})
      :fx dispatchs})))

(rf/reg-event-fx
 ::audit->set-filtered-sessions-by-id
 (fn
   [{:keys [db]} [_ session session-id-list]]
   (let [new-filtered-sessions-by-id (concat [session] (:data (:audit->filtered-session-by-id db)))
         finished? (if (= (count session-id-list) (count new-filtered-sessions-by-id))
                     true
                     false)]
     {:db (assoc db :audit->filtered-session-by-id {:data new-filtered-sessions-by-id
                                                    :status (if finished?
                                                              :ready
                                                              :loading)})})))

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
         event-stream (if (= "exec" (:verb session))
                        "event_stream=base64"
                        (if (= "postgres" (:connection_subtype session))
                          "event_stream=raw-queries"
                          ""))]
     (if (and event-size (> event-size size-threshold))
       {:db (assoc db
                   :audit->session-details
                   {:session session
                    :status :success
                    :has-large-payload? true})}
       {:fx [[:dispatch [:fetch
                         {:method "GET"
                          :uri (str "/sessions/" (:id session) "?expand=event_stream&" event-stream)
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
         updated-session (merge cached-session details)]
     {:db (assoc db
                 :audit->session-details
                 {:session updated-session
                  :status :success
                  :has-large-payload? false
                  :session-logs (:session-logs (:audit->session-details db))})})))

(rf/reg-event-fx
 :audit->clear-session
 (fn
   [{:keys [db]} [_]]
   {:db (assoc db :audit->session-details {:status :loading
                                           :session nil
                                           :session-logs {:status :loading}})}))

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
   [{:keys [db]} [_ session status]]
   {:fx [[:dispatch
          [:fetch {:method "PUT"
                   :uri (str "/reviews/" (-> session :review :id))
                   :body {:status status}
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
                                                              :details %}])}]]]}))

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
