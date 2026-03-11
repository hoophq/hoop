(ns webapp.connections.native-client-access.events
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.connections.native-client-access.constants :as constants]
   [webapp.connections.native-client-access.main :as native-client-access-main]
   [webapp.audit.views.session-details :as session-details]))


(rf/reg-fx
 :open-rdp-web-client
 (fn [{:keys [username]}]
   (let [form (.createElement js/document "form")
         input (.createElement js/document "input")]
     (set! (.-method form) "POST")
     (set! (.-action form) "/rdpproxy/client")
     (set! (.-target form) "_blank")
     (set! (.-type input) "hidden")
     (set! (.-name input) "credential")
     (set! (.-value input) username)
     (.appendChild form input)
     (.appendChild (.-body js/document) form)
     (.submit form)
     (.remove form))))

;; Get native client access for a connection
(rf/reg-event-fx
 :native-client-access->request-access
 (fn [{:keys [db]} [_ connection-name-or-id access-duration-minutes]]
   (let [access-duration-seconds (constants/minutes->seconds access-duration-minutes)]
     {:db (update-in db [:native-client-access :requesting-connections] conj connection-name-or-id)
      :fx [[:dispatch [:fetch {:method "POST"
                               :uri (str "/connections/" connection-name-or-id "/credentials")
                               :body {:access_duration_seconds access-duration-seconds}
                               :on-success #(rf/dispatch [:native-client-access->request-success connection-name-or-id %])
                               :on-failure #(rf/dispatch [:native-client-access->request-failure connection-name-or-id %])}]]]})))

;; Handle successful native client access response
(rf/reg-event-fx
 :native-client-access->request-success
 (fn [{:keys [db]} [_ connection-name response]]
   (if (:has_review response)
     ;; Has review - close current modal and open session details modal
     {:db (update-in db [:native-client-access :requesting-connections] disj connection-name)
      :fx [[:dispatch [:modal->close]]
           [:dispatch-later {:ms 100
                             :dispatch [:modal->open {:id "session-details"
                                                     :maxWidth "95vw"
                                                     :content [session-details/main {:id (:session_id response) :verb "connect"}]}]}]
           [:dispatch [:show-snackbar {:level :info
                                       :text "This connection requires review approval"}]]]}
     ;; No review - existing flow
     (let [connection-name-key (:connection_name response)]
       ;; Save to localStorage (add to sessions map)
       (constants/save-session connection-name-key response)

       {:db (-> db
                (update-in [:native-client-access :requesting-connections] disj connection-name)
                (assoc-in [:native-client-access :sessions connection-name-key] response))
        :fx [[:dispatch [:show-snackbar {:level :success
                                         :text "Native client access granted successfully!"}]]]}))))

;; Handle failed native client access response
(rf/reg-event-fx
 :native-client-access->request-failure
 (fn [{:keys [db]} [_ connection-name error]]
   (let [is-admin? (get-in db [:users->current-user :data :admin?])
         error-message (or (:message error)
                           (get-in error [:response :message])
                           "Failed to request native client access")]
     {:db (update-in db [:native-client-access :requesting-connections] disj connection-name)
      :fx (if is-admin?
            ;; Admin: Show backend error in snackbar
            [[:dispatch [:modal->close]]
             [:dispatch [:show-snackbar {:level :error
                                         :text (str/capitalize error-message)}]]]
            ;; Non-admin: Show error dialog with friendly message
            [(let [error-message (get-in constants/error-messages
                                         [:generic :non-admin])]
               [:dispatch [:modal->open {:content [native-client-access-main/not-available-dialog
                                                   {:error-message error-message
                                                    :user-is-admin? is-admin?}]
                                         :maxWidth "446px"}]])])})))

;; Start native client access flow - use integrated layout
(rf/reg-event-fx
 :native-client-access->start-flow
 (fn [{:keys [db]} [_ connection-name]]
   ;; First check if agent is online before proceeding
   {:db (assoc-in db [:native-client-access :checking-agent?] true)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/connections/" connection-name)
                             :on-success #(rf/dispatch [:native-client-access->agent-status-check-success % connection-name])
                             :on-failure #(rf/dispatch [:native-client-access->agent-status-check-failure % connection-name])}]]]}))

;; Handle agent status check success
(rf/reg-event-fx
 :native-client-access->agent-status-check-success
 (fn [{:keys [db]} [_ response connection-name]]
   (let [is-online? (= (:status response) "online")
         is-admin? (get-in db [:users->current-user :data :admin?])]
     {:db (assoc-in db [:native-client-access :checking-agent?] false)
      :fx [(if is-online?
             ;; Agent is online - proceed with normal flow, passing full response for JIT info
             [:dispatch [:modal->open {:content [native-client-access-main/main response]
                                       :custom-on-click-out #(native-client-access-main/minimize-modal connection-name)
                                       :maxWidth "1100px"}]]
             ;; Agent is offline - show error dialog
             (let [error-message (get-in constants/error-messages
                                         [:agent-offline (if is-admin? :admin :non-admin)])]
               [:dispatch [:modal->open {:content [native-client-access-main/not-available-dialog
                                                   {:error-message error-message
                                                    :user-is-admin? is-admin?}]
                                         :maxWidth "446px"}]]))]})))

;; Handle agent status check failure
(rf/reg-event-fx
 :native-client-access->agent-status-check-failure
 (fn [{:keys [db]} [_ _error _connection]]
   (let [is-admin? (get-in db [:users->current-user :data :admin?])
         error-message (get-in constants/error-messages
                               [:agent-offline (if is-admin? :admin :non-admin)])]
     {:db (assoc-in db [:native-client-access :checking-agent?] false)
      :fx [[:dispatch [:modal->open {:content [native-client-access-main/not-available-dialog
                                               {:error-message error-message
                                                :user-is-admin? is-admin?}]
                                     :maxWidth "446px"}]]]})))

;; Clear specific native client access session
(rf/reg-event-fx
 :native-client-access->clear-session
 (fn [{:keys [db]} [_ connection-name]]
   ;; Remove from localStorage
   (constants/remove-session connection-name)

   {:db (update-in db [:native-client-access :sessions] dissoc connection-name)
    :fx [[:dispatch [:draggable-cards->close connection-name]]]}))

;; Reopen main connect modal (used by draggable card expand)
(rf/reg-event-fx
 :native-client-access->reopen-connect-modal
 (fn [_ [_ connection-name]]
   {:fx [[:dispatch [:modal->open {:content [native-client-access-main/main connection-name]
                                   :maxWidth "1100px"
                                   :custom-on-click-out #(native-client-access-main/minimize-modal connection-name)}]]]}))

;; Check for active native client access sessions on app initialization
(rf/reg-event-fx
 :native-client-access->check-active-sessions
 (fn [{:keys [db]} [_]]
   (let [all-sessions (constants/get-all-sessions)
         valid-sessions (into {} (filter (fn [[_ session]]
                                           (constants/native-client-access-valid? session))
                                         all-sessions))]
     (if (empty? valid-sessions)
       {}
       {:db (assoc-in db [:native-client-access :sessions] valid-sessions)
        :fx (vec (for [[connection-name session-data] valid-sessions]
                   [:dispatch-later {:ms 1000
                                     :dispatch [:native-client-access->show-active-session connection-name session-data]}]))}))))

;; Show draggable card for active session
(rf/reg-event-fx
 :native-client-access->show-active-session
 (fn [_ [_ connection-name session-data]]
   {:fx [[:dispatch [:draggable-cards->open
                     connection-name
                     {:component [native-client-access-main/minimize-modal-content connection-name session-data]
                      :on-click-expand (fn []
                                         (rf/dispatch [:draggable-cards->close connection-name])
                                         (rf/dispatch [:native-client-access->reopen-connect-modal connection-name]))}]]]}))

;; Auto-cleanup expired session on app initialization
(rf/reg-event-fx
 :native-client-access->cleanup-all-expired
 (fn [{:keys [db]} [_]]
   (let [valid-sessions (constants/cleanup-expired-sessions)]
     {:db (assoc-in db [:native-client-access :sessions] valid-sessions)})))

;; Subscriptions
(rf/reg-sub
 :native-client-access->requesting?
 (fn [db [_ connection-name]]
   (contains? (get-in db [:native-client-access :requesting-connections] #{}) connection-name)))

(rf/reg-sub
 :native-client-access->current-session
 (fn [db [_ connection-name]]
   (get-in db [:native-client-access :sessions connection-name])))

(rf/reg-sub
 :native-client-access->session-valid?
 (fn [db [_ connection-name]]
   (let [session (get-in db [:native-client-access :sessions connection-name])]
     (constants/native-client-access-valid? session))))

;; Event to open RDP web client
(rf/reg-event-fx
 :native-client-access->open-rdp-web-client
 (fn [_ [_ username]]
   {:open-rdp-web-client {:username username}}))

;; Resume credentials request after review approval
(rf/reg-event-fx
 :native-client-access->resume-credentials
 (fn [{:keys [db]} [_ connection-name session-id on-error-cb]]
   ;; Get access duration from the session's review
   (let [session (get-in db [:audit->session-details :session])
         access-duration-sec (get-in session [:review :access_duration_sec])]
     {:fx [[:dispatch [:fetch {:method "POST"
                               :uri (str "/connections/" connection-name "/credentials/" session-id)
                               :body {:access_duration_seconds access-duration-sec}
                               :on-success #(rf/dispatch [:native-client-access->resume-success connection-name %])
                               :on-failure (fn [error]
                                             (when on-error-cb (on-error-cb))
                                             (rf/dispatch [:native-client-access->resume-failure error]))}]]]})))

;; Handle successful resume of credentials
(rf/reg-event-fx
 :native-client-access->resume-success
 (fn [{:keys [db]} [_ connection-name response]]
   (let [connection-name-key (:connection_name response)]
     ;; Save credentials to localStorage and db
     (constants/save-session connection-name-key response)
     
     {:db (assoc-in db [:native-client-access :sessions connection-name-key] response)
      :fx [[:dispatch [:modal->close]]
           [:dispatch [:native-client-access->reopen-connect-modal connection-name]]
           [:dispatch [:show-snackbar {:level :success
                                       :text "Credentials obtained successfully!"}]]]})))

;; Handle failed resume of credentials
(rf/reg-event-fx
 :native-client-access->resume-failure
 (fn [_ [_ error]]
   (let [error-message (or (:message error)
                          (get-in error [:response :message])
                          "Failed to obtain credentials")]
     {:fx [[:dispatch [:show-snackbar {:level :error
                                       :text error-message}]]]})))

