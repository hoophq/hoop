(ns webapp.connections.native-client-access.events
  (:require
   [cljs.reader :as reader]
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.connections.native-client-access.constants :as constants]
   [webapp.connections.native-client-access.main :as native-client-access-main]))

;; Get native client access for a connection
(rf/reg-event-fx
 :native-client-access->request-access
 (fn [{:keys [db]} [_ connection-name-or-id access-duration-minutes]]
   (let [access-duration-seconds (constants/minutes->seconds access-duration-minutes)]
     {:db (assoc-in db [:native-client-access :requesting?] true)
      :fx [[:dispatch [:fetch {:method "POST"
                               :uri (str "/connections/" connection-name-or-id "/credentials")
                               :body {:access_duration_seconds access-duration-seconds}
                               :on-success #(rf/dispatch [:native-client-access->request-success %])
                               :on-failure #(rf/dispatch [:native-client-access->request-failure %])}]]]})))

;; Handle successful native client access response
(rf/reg-event-fx
 :native-client-access->request-success
 (fn [{:keys [db]} [_ response]]
   ;; Save to localStorage (replacing any existing session)
   (.setItem js/localStorage constants/native-client-access-storage-key (pr-str response))

   {:db (update db :native-client-access merge {:requesting? false :current response})
    :fx [[:dispatch [:show-snackbar {:level :success
                                     :text "Native client access granted successfully!"}]]
         ;; No need to open new modal - the main component handles the flow
         ]}))

;; Handle failed native client access response
(rf/reg-event-fx
 :native-client-access->request-failure
 (fn [{:keys [db]} [_ error]]
   (let [is-admin? (get-in db [:users->current-user :data :admin?])
         error-message (or (:message error)
                           (get-in error [:response :message])
                           "Failed to request native client access")]
     {:db (assoc-in db [:native-client-access :requesting?] false)
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

;; Clean up expired or invalid native client access data
(rf/reg-event-fx
 :native-client-access->cleanup-expired
 (fn [{:keys [db]} [_]]
   (.removeItem js/localStorage constants/native-client-access-storage-key)
   {:db (assoc-in db [:native-client-access :current] nil)}))

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
             ;; Agent is online - proceed with normal flow
             [:dispatch [:modal->open {:content [native-client-access-main/main connection-name]
                                       :custom-on-click-out native-client-access-main/minimize-modal
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

;; Clear current native client access session
(rf/reg-event-fx
 :native-client-access->clear-session
 (fn [{:keys [db]} [_]]
   (.removeItem js/localStorage constants/native-client-access-storage-key)
   {:db (assoc-in db [:native-client-access :current] nil)}))

;; Reopen main connect modal (used by draggable card expand)
(rf/reg-event-fx
 :native-client-access->reopen-connect-modal
 (fn [{:keys [db]} [_]]
   ;; Get connection from current session data
   (let [current-session (get-in db [:native-client-access :current])
         connection {:name (:id current-session)
                     :connection_name (:connection_name current-session)}]
     {:fx [[:dispatch [:modal->open {:content [native-client-access-main/main connection]
                                     :maxWidth "1100px"
                                     :custom-on-click-out native-client-access-main/minimize-modal}]]]})))

;; Check for active native client access sessions on app initialization
(rf/reg-event-fx
 :native-client-access->check-active-sessions
 (fn [{:keys [db]} [_]]
   (let [stored-data (.getItem js/localStorage constants/native-client-access-storage-key)]
     (if stored-data
       (try
         (let [parsed-data (reader/read-string stored-data)]
           (if (constants/native-client-access-valid? parsed-data)
             ;; Found active session - load it and show draggable card
             {:db (assoc-in db [:native-client-access :current] parsed-data)
              :fx [[:dispatch-later {:ms 1000 ; Wait for app to be ready
                                     :dispatch [:native-client-access->show-active-session parsed-data]}]]}
             ;; Session expired, clean it up
             {:fx [[:dispatch [:native-client-access->cleanup-expired]]]}))
         (catch js/Error _
           ;; Invalid data, clean it up
           {:fx [[:dispatch [:native-client-access->cleanup-expired]]]}))
       ;; No stored session
       {}))))

;; Show draggable card for active session
(rf/reg-event-fx
 :native-client-access->show-active-session
 (fn [_ [_ session-data]]
   {:fx [[:dispatch [:draggable-card->open
                     {:component [native-client-access-main/minimize-modal-content session-data]
                      :on-click-expand (fn []
                                         (rf/dispatch [:draggable-card->close])
                                         (rf/dispatch [:native-client-access->reopen-connect-modal]))}]]]}))

;; Auto-cleanup expired session on app initialization
(rf/reg-event-fx
 :native-client-access->cleanup-all-expired
 (fn [_ [_]]
   (let [stored-data (.getItem js/localStorage constants/native-client-access-storage-key)]
     (when stored-data
       (try
         (let [parsed-data (reader/read-string stored-data)]
           (when-not (constants/native-client-access-valid? parsed-data)
             (.removeItem js/localStorage constants/native-client-access-storage-key)))
         (catch js/Error _
           ;; Invalid data, remove it
           (.removeItem js/localStorage constants/native-client-access-storage-key))))
     {})))

;; Subscriptions
(rf/reg-sub
 :native-client-access->requesting?
 (fn [db _]
   (get-in db [:native-client-access :requesting?] false)))

(rf/reg-sub
 :native-client-access->current-session
 (fn [db _]
   (get-in db [:native-client-access :current])))

(rf/reg-sub
 :native-client-access->session-valid?
 (fn [db _]
   (let [current-session (get-in db [:native-client-access :current])]
     (constants/native-client-access-valid? current-session))))
