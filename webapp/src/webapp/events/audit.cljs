(ns webapp.events.audit
  (:require
   [re-frame.core :as rf]
   [clojure.string :as string]))

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
                                                :uri (str "/sessions/" session-id "?event_stream=utf8")
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
                :session-logs {:status :loading}}]
     {:db (assoc db :audit->session-details state)
      :fx [[:dispatch [:fetch
                       {:method "GET"
                        :uri (str "/sessions/" (:id session) "?event_stream=utf8")
                        :on-success #(rf/dispatch [:audit->check-session-size %])}]]]})))

(def size-threshold (* 4 1024 1024)) ; 1MB threshold

(rf/reg-event-fx
 :audit->check-session-size
 (fn
   [{:keys [db]} [_ session]]
   (let [event-size (:event_size session)]
     (if (and event-size (> event-size size-threshold))
       {:db (assoc db
                   :audit->session-details
                   {:session session
                    :status :success
                    :has-large-payload? true})}
       {:fx [[:dispatch [:fetch
                         {:method "GET"
                          :uri (str "/sessions/" (:id session) "?event_stream=utf8&expand=event_stream")
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
                       (rf/dispatch [:show-snackbar {:text error :level :error}]))))]
     {:fx [[:dispatch [:fetch (merge
                               {:method "POST"
                                :uri (str "/sessions/" (:id session) "/exec")
                                :on-success success
                                :on-failure failure})]]]})))

(rf/reg-event-fx
 :audit->re-run-session
 (fn
   [{:keys [db]} [_ session]]
   (let [payload {:script (-> session :script :data)
                  :labels {:re-run-from (:id session)}
                  :connection (:connection session)}
         success (fn [res]
                   (js/setTimeout
                    (fn []
                      (rf/dispatch
                       [:show-snackbar {:level :success
                                        :text "The session was re-ran"}])
                      (rf/dispatch [:audit->get-sessions])
                      (rf/dispatch [:audit->get-session-by-id {:id (:session_id res) :verb "exec"}]))
                    800))
        ;; The failure function can call the success due to an API behaviour
        ;; when a session is badly executed, it doesn't mean it wasn't executed
        ;; so the API may respond a bad request because of the execution error
        ;; in the agent, but a session was generated with a log.
         failure (fn [error res]
                   (let [session-id (:session_id res)]
                     (if (and session-id (> (count session-id) 0))
                       (success res)
                       (rf/dispatch [:show-snackbar {:text error :level :error}]))))]
     {:fx [[:dispatch [:fetch {:method "POST"
                               :uri "/sessions"
                               :on-success success
                               :on-failure failure
                               :body payload}]]]})))

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
                                    :text (str "Your review was added")}])
                     (js/setTimeout
                      (fn []
                        (rf/dispatch [:audit->get-sessions])
                        (rf/dispatch [:audit->get-session-by-id session]))
                      500))
                   :on-failure #(rf/dispatch [:show-snackbar {:text %
                                                              :level :error}])}]]]}))

(rf/reg-event-fx
 :audit->session-file-generate
 (fn
   [{:keys [db]} [_ session-id extension]]
   (let [success (fn [res] (.open js/window (:download_url res)))
         failure (fn [error]
                   (rf/dispatch [:show-snackbar {:text error :level :error}]))]
     {:fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/sessions/"
                                         session-id
                                         "?extension="
                                         extension)
                               :on-success success
                               :on-failure failure}]]]})))
