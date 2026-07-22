(ns webapp.events.audit
  (:require
   [clojure.string :as string]
   [re-frame.core :as rf]
   [webapp.config :as config]
   [webapp.formatters :as formatters]
   [webapp.jira-templates.loading-jira-templates :as loading-jira-templates]))

;; Active SSE AbortControllers keyed by session id. Kept outside re-frame.db
;; because AbortController is a non-serializable JS object.
(defonce ^:private sse-controllers (atom {}))

(defn- parse-sse-block
  "Turn an SSE message block (lines separated by '\\n') into a map with
  :event and :data keys. Lines starting with ':' are SSE comments and ignored.
  Multiple 'data:' lines are concatenated with '\\n' per the SSE spec."
  [block]
  (reduce
   (fn [acc line]
     (cond
       (string/starts-with? line ":") acc
       (string/starts-with? line "event:")
       (assoc acc :event (string/trim (subs line 6)))
       (string/starts-with? line "data:")
       (let [chunk (string/trim (subs line 5))]
         (update acc :data (fn [d] (if d (str d "\n" chunk) chunk))))
       :else acc))
   {:event "message" :data nil}
   (array-seq (.split block "\n"))))

(defn- parse-sse-buffer
  "Pull complete SSE messages out of `buffer`. Returns [parsed-events leftover].
  Messages are delimited by a blank line ('\\n\\n')."
  [buffer]
  (loop [s buffer
         out []]
    (let [idx (.indexOf s "\n\n")]
      (if (neg? idx)
        [out s]
        (let [block (.substring s 0 idx)
              rest-s (.substring s (+ idx 2))
              parsed (parse-sse-block block)]
          (recur rest-s
                 (if (:data parsed) (conj out parsed) out)))))))

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
         live-connect? (and (= "connect" (:verb session))
                            (= "open" (:status session)))
         event-stream (cond
                        (= "exec" (:verb session))
                        "event_stream=base64"

                        ;; Live connect session: keep raw wire frames so the
                        ;; client-side decoder used by session-live-tail can
                        ;; render historical and streamed events uniformly.
                        live-connect?
                        ""

                        (= "postgres" (:connection_subtype session))
                        "event_stream=raw-queries"

                        :else "")
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
                     :has-large-input? has-large-input?}
         is-exec? (= "exec" (:verb session))]
     (if (and has-large-event? has-large-input?)
       {:db (assoc db :audit->session-details base-state)
        :fx (cond-> []
              is-exec? (conj [:dispatch [:audit->get-session-stream-result (:id session)]]))}
       {:db (assoc db :audit->session-details (assoc base-state :status :loading))
        :fx (cond-> [[:dispatch [:fetch
                                 {:method "GET"
                                  :uri (str "/sessions/" (:id session) query-string)
                                  :on-success (fn [session-data]
                                                (rf/dispatch [:audit->set-session session-data])
                                                (rf/dispatch [:reports->get-report-by-session-id session-data]))}]]]
              (and has-large-event? is-exec?) (conj [:dispatch [:audit->get-session-stream-result (:id session)]]))}))))

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
 :audit->get-session-stream-result
 (fn
   [{:keys [db]} [_ session-id]]
   {:db (assoc db :audit->session-stream-result {:status :loading :data nil})
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri (str "/sessions/" session-id "/result/stream")
                      :on-success (fn [data]
                                    (rf/dispatch [:audit->set-session-stream-result data nil]))
                      :on-failure (fn [_]
                                    (rf/dispatch [:audit->set-session-stream-result nil :error]))}]]]}))

(rf/reg-event-db
 :audit->set-session-stream-result
 (fn
   [db [_ data status]]
   (assoc db :audit->session-stream-result
          {:status (or status :success)
           :data data})))

(rf/reg-event-fx
 :audit->clear-session
 (fn
   [{:keys [db]} [_]]
   {:db (-> db
            (assoc :audit->session-details {:status :loading
                                            :session nil
                                            :session-logs {:status :loading}})
            (assoc :audit->session-logs {:status :idle :data nil})
            (assoc :audit->session-stream-result {:status :idle :data nil}))}))

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
   (let [session-id (:id session)]
     ;; no-op when an execution is already tracked for this session, so a
     ;; double click never issues a duplicate POST
     (if (contains? #{:executing :running} (get-in db [:audit->execution session-id :status]))
       {}
       {:db (assoc-in db [:audit->execution session-id] {:status :executing})
        :fx [[:dispatch [:fetch {:method "POST"
                                 :uri (str "/sessions/" session-id "/exec")
                                 :on-success #(rf/dispatch [:audit->execute-session-success session-id %])
                                 :on-failure #(rf/dispatch [:audit->execute-session-failure session-id %])}]]]}))))

(rf/reg-event-fx
 :audit->execute-session-success
 (fn
   [{:keys [db]} [_ session-id res]]
   (if (= (:output_status res) "running")
     ;; HTTP 202: the gateway stopped waiting after 50s but the execution
     ;; keeps running in the agent; poll the session until it finishes
     {:db (assoc-in db [:audit->execution session-id] {:status :running})
      :fx [[:dispatch-later {:ms 5000 :dispatch [:audit->watch-running-session session-id]}]]}
     {:db (assoc-in db [:audit->execution session-id] {:status :done})
      :fx [[:dispatch-later {:ms 800 :dispatch [:audit->get-sessions]}]
           [:dispatch-later {:ms 800 :dispatch [:audit->get-session-by-id {:id session-id :verb "exec"}]}]]})))

(rf/reg-event-fx
 :audit->execute-session-failure
 (fn
   [{:keys [db]} [_ session-id error]]
   ;; refetch the session so the Execute gate re-evaluates against the
   ;; server state (e.g. a 403 after the review left the APPROVED status)
   {:db (update db :audit->execution dissoc session-id)
    :fx [[:dispatch [:audit->get-session-by-id {:id session-id :verb "exec"}]]
         [:dispatch [:show-snackbar {:text "Failed to execute script"
                                     :level :error
                                     :details error}]]]}))

;; poll every 5s for the first ~5 minutes, then back off to 30s so an
;; execution that never settles does not keep a tight polling loop forever
(defn- watch-poll-interval-ms [attempts]
  (if (< attempts 60) 5000 30000))

(rf/reg-event-fx
 :audit->watch-running-session
 (fn
   [{:keys [db]} [_ session-id]]
   ;; the :running guard stops the loop when the execution state changes
   (if (= :running (get-in db [:audit->execution session-id :status]))
     {:db (update-in db [:audit->execution session-id :attempts] (fnil inc 0))
      :fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/sessions/" session-id)
                               :on-success #(rf/dispatch [:audit->watch-running-session-check session-id %])
                               ;; transient errors: keep polling
                               :on-failure #(rf/dispatch [:audit->watch-running-session-check session-id nil])}]]]}
     {})))

(rf/reg-event-fx
 :audit->watch-running-session-check
 (fn
   [{:keys [db]} [_ session-id session]]
   (if (= "done" (:status session))
     {:db (assoc-in db [:audit->execution session-id] {:status :done})
      :fx [[:dispatch [:audit->get-sessions]]
           [:dispatch [:audit->get-session-by-id {:id session-id :verb "exec"}]]]}
     (let [attempts (get-in db [:audit->execution session-id :attempts] 0)]
       {:fx [[:dispatch-later {:ms (watch-poll-interval-ms attempts)
                               :dispatch [:audit->watch-running-session session-id]}]]}))))

(rf/reg-event-fx
 :audit->ensure-running-session-watch
 (fn
   [{:keys [db]} [_ session-id]]
   ;; resumes tracking a reviewed exec that is running server-side but has no
   ;; local state (e.g. after a page reload); no-op when already tracked
   (if (get-in db [:audit->execution session-id])
     {}
     {:db (assoc-in db [:audit->execution session-id] {:status :running})
      :fx [[:dispatch [:audit->watch-running-session session-id]]]})))

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
   (let [payload (cond-> {:script (-> session :script :data)
                          :labels {:re-run-from (:id session)}
                          :connection (:connection session)}
                   (not (string/blank? (:correlation_id session)))
                   (assoc :correlation_id (:correlation_id session)))

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
                                  force-review
                                  rejection-reason]}]]
   (let [body (cond-> {:status (string/upper-case status)}
                (and start-time end-time)
                (assoc :time_window {:type "time_range"
                                     :configuration {:start_time (formatters/local-time->utc-time start-time)
                                                     :end_time (formatters/local-time->utc-time end-time)}})

                force-review
                (assoc :force_review true)

                (not (string/blank? rejection-reason))
                (assoc :rejection_reason rejection-reason))]
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

;; ─── Server-Sent Events for live connect sessions ──────────────────────────
;;
;; Interactive connect-verb sessions with status=open stream their audit
;; events in real time via GET /sessions/<id>/stream (SSE). We open the
;; stream when the session-details modal is rendered for a live connect
;; session and close it on unmount / when the backend signals session_end.

(rf/reg-fx
 :audit->sse-start
 (fn [{:keys [session-id]}]
   (when (and session-id (not (contains? @sse-controllers session-id)))
     (let [controller (js/AbortController.)
           token (.getItem js/localStorage "jwt-token")
           url (str config/api "/sessions/" session-id "/stream")]
       (swap! sse-controllers assoc session-id controller)
       (rf/dispatch [:audit->set-stream-state session-id :connecting])
       (-> (js/fetch url
                     #js {:method "GET"
                          :headers #js {:Authorization (str "Bearer " token)
                                        :Accept "text/event-stream"}
                          :signal (.-signal controller)
                          :cache "no-store"})
           (.then
            (fn [response]
              (if-not (.-ok response)
                (do
                  (rf/dispatch [:audit->set-stream-state session-id :error])
                  (swap! sse-controllers dissoc session-id))
                (let [reader (.getReader (.-body response))
                      decoder (js/TextDecoder.)
                      buf (atom "")]
                  (rf/dispatch [:audit->set-stream-state session-id :live])
                  (letfn [(pump []
                            (-> (.read reader)
                                (.then
                                 (fn [result]
                                   (if (.-done result)
                                     (do
                                       (rf/dispatch [:audit->session-stream-ended session-id])
                                       (swap! sse-controllers dissoc session-id))
                                     (do
                                       (swap! buf str (.decode decoder (.-value result)))
                                       (let [[events leftover] (parse-sse-buffer @buf)]
                                         (reset! buf leftover)
                                         (doseq [ev events]
                                           (rf/dispatch [:audit->session-stream-event
                                                         session-id ev])))
                                       (pump)))))
                                (.catch
                                 (fn [err]
                                   (when (not= (.-name err) "AbortError")
                                     (js/console.warn "SSE read error:" err)
                                     (rf/dispatch [:audit->set-stream-state session-id :error]))
                                   (swap! sse-controllers dissoc session-id)))))]
                    (pump))))))
           (.catch
            (fn [err]
              (when (not= (.-name err) "AbortError")
                (js/console.warn "SSE connect error:" err)
                (rf/dispatch [:audit->set-stream-state session-id :error]))
              (swap! sse-controllers dissoc session-id))))))))

(rf/reg-fx
 :audit->sse-stop
 (fn [session-id]
   (when-let [ctrl (get @sse-controllers session-id)]
     (.abort ctrl)
     (swap! sse-controllers dissoc session-id))))

(rf/reg-event-fx
 :audit->session-stream-subscribe
 (fn [_ [_ session-id]]
   {:audit->sse-start {:session-id session-id}}))

(rf/reg-event-fx
 :audit->session-stream-unsubscribe
 (fn [_ [_ session-id]]
   ;; Only tear down the underlying fetch — keep whatever state
   ;; `session-stream-ended` (or the next subscribe) wrote so the live tail
   ;; can keep showing "Ended" after the SSE closes.
   {:audit->sse-stop session-id}))

(rf/reg-event-db
 :audit->set-stream-state
 (fn [db [_ session-id state]]
   (assoc-in db [:audit->session-stream session-id :state] state)))

(rf/reg-event-fx
 :audit->session-stream-event
 (fn [{:keys [db]} [_ session-id ev]]
   (let [current-session (-> db :audit->session-details :session)
         applies? (= (:id current-session) session-id)]
     (cond
       (not applies?) {}

       (= (:event ev) "session_end")
       {:fx [[:dispatch [:audit->session-stream-ended session-id]]]}

       (= (:event ev) "event")
       (let [parsed (try
                      (js->clj (js/JSON.parse (:data ev)) :keywordize-keys true)
                      (catch js/Object _ nil))]
         (if parsed
           (let [start-date (:start_date current-session)
                 ev-time (:time parsed)
                 start-ms (when start-date (.getTime (js/Date. start-date)))
                 ev-ms (when ev-time (.getTime (js/Date. ev-time)))
                 seconds (if (and start-ms ev-ms (pos? start-ms))
                           (/ (- ev-ms start-ms) 1000.0)
                           0)
                 entry [seconds (:type parsed) (:payload parsed)]]
             {:db (update-in db [:audit->session-details :session :event_stream]
                             (fnil conj []) entry)})
           {}))

       :else {}))))

(rf/reg-event-fx
 :audit->session-stream-ended
 (fn [{:keys [db]} [_ session-id]]
   (let [current-session (-> db :audit->session-details :session)
         applies? (= (:id current-session) session-id)
         was-live? (= :live (get-in db [:audit->session-stream session-id :state]))
         db' (cond-> (assoc-in db [:audit->session-stream session-id :state] :ended)
               applies? (assoc-in [:audit->session-details :session :status] "done")
               applies? (assoc-in [:audit->session-details :session :end_date]
                                  (.toISOString (js/Date.))))]
     (cond-> {:db db'
              :audit->sse-stop session-id}
       ;; Refresh the sessions list so its "Live" badge for this session
       ;; clears. We only do this if the stream actually went live — for an
       ;; already-ended session we wouldn't have anything new to show.
       was-live?
       (assoc :fx [[:dispatch [:audit->get-sessions]]])))))
