(ns webapp.events.db-access
  "Database Access Event Handlers

  Error Handling Strategy:

  1. AGENT OFFLINE (checked first):
     - Admin & Non-admin: Both get error dialog with specific messages
     - Uses db-access-not-available-dialog component

  2. BACKEND ERRORS (review, proxy-port, etc.):
     - Admin: Gets real backend error in snackbar (technical details)
     - Non-admin: Gets friendly error dialog (user-friendly message)

  Flow:
  1. User clicks 'Open in Native Client'
  2. Check agent status first (:db-access->agent-status-check-*)
  3. If agent online, proceed to request access (:db-access->request-access)
  4. Backend validates (review, proxy-port, permissions, etc.)
  5. Success: Show integrated modal | Failure: Show error (snackbar vs dialog)
  "
  (:require
   [cljs.reader :as reader]
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.connections.constants.db-access :as db-access-constants]
   [webapp.connections.views.db-access-not-available-dialog :as db-access-not-available-dialog]
   [webapp.connections.db-access.main :as db-access-main]))

;; Get database access for a connection
(rf/reg-event-fx
 :db-access->request-access
 (fn [{:keys [db]} [_ connection-name-or-id access-duration-minutes]]
   (let [access-duration-seconds (db-access-constants/minutes->seconds access-duration-minutes)]
     {:db (assoc-in db [:db-access :requesting?] true)
      :fx [[:dispatch [:fetch {:method "POST"
                               :uri (str "/connections/" connection-name-or-id "/credentials")
                               :body {:access_duration_seconds access-duration-seconds}
                               :on-success #(rf/dispatch [:db-access->request-success %])
                               :on-failure #(rf/dispatch [:db-access->request-failure %])}]]]})))

;; Handle successful database access response
(rf/reg-event-fx
 :db-access->request-success
 (fn [{:keys [db]} [_ response]]
   ;; Save to localStorage (replacing any existing session)
   (.setItem js/localStorage db-access-constants/db-access-storage-key (pr-str response))

   {:db (-> db
            (assoc-in [:db-access :requesting?] false)
            (assoc-in [:db-access :current] response))
    :fx [[:dispatch [:show-snackbar {:level :success
                                     :text "Database access granted successfully!"}]]
         ;; No need to open new modal - the main component handles the flow
         ]}))

;; Handle failed database access response
(rf/reg-event-fx
 :db-access->request-failure
 (fn [{:keys [db]} [_ error]]
   (let [is-admin? (get-in db [:users->current-user :data :admin?])
         error-message (or (:message error)
                           (get-in error [:response :message])
                           "Failed to request database access")]
     {:db (assoc-in db [:db-access :requesting?] false)
      :fx (if is-admin?
            ;; Admin: Show backend error in snackbar
            [[:dispatch [:modal->close]]
             [:dispatch [:show-snackbar {:level :error
                                         :text (str/capitalize error-message)}]]]
            ;; Non-admin: Show error dialog with friendly message
            [(let [error-message (get-in db-access-constants/error-messages
                                         [:generic :non-admin])]
               [:dispatch [:modal->open {:content [db-access-not-available-dialog/main
                                                   {:error-message error-message
                                                    :user-is-admin? is-admin?}]
                                         :maxWidth "446px"}]])])})))

;; Clean up expired or invalid database access data
(rf/reg-event-fx
 :db-access->cleanup-expired
 (fn [{:keys [db]} [_]]
   (.removeItem js/localStorage db-access-constants/db-access-storage-key)
   {:db (assoc-in db [:db-access :current] nil)}))

;; Start database access flow - use integrated layout
(rf/reg-event-fx
 :db-access->start-flow
 (fn [{:keys [db]} [_ connection-name]]
   ;; First check if agent is online before proceeding
   {:db (assoc-in db [:db-access :checking-agent?] true)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/connections/" connection-name)
                             :on-success #(rf/dispatch [:db-access->agent-status-check-success % connection-name])
                             :on-failure #(rf/dispatch [:db-access->agent-status-check-failure % connection-name])}]]]}))

;; Handle agent status check success
(rf/reg-event-fx
 :db-access->agent-status-check-success
 (fn [{:keys [db]} [_ response connection-name]]
   (let [is-online? (= (:status response) "online")
         is-admin? (get-in db [:users->current-user :data :admin?])]
     {:db (assoc-in db [:db-access :checking-agent?] false)
      :fx [(if is-online?
             ;; Agent is online - proceed with normal flow
             [:dispatch [:modal->open {:content [db-access-main/main connection-name]
                                       :custom-on-click-out db-access-main/minimize-modal
                                       :maxWidth "1100px"}]]
             ;; Agent is offline - show error dialog
             (let [error-message (get-in db-access-constants/error-messages
                                         [:agent-offline (if is-admin? :admin :non-admin)])]
               [:dispatch [:modal->open {:content [db-access-not-available-dialog/main
                                                   {:error-message error-message
                                                    :user-is-admin? is-admin?}]
                                         :maxWidth "446px"}]]))]})))

;; Handle agent status check failure
(rf/reg-event-fx
 :db-access->agent-status-check-failure
 (fn [{:keys [db]} [_ _error _connection]]
   (let [is-admin? (get-in db [:users->current-user :data :admin?])
         error-message (get-in db-access-constants/error-messages
                               [:agent-offline (if is-admin? :admin :non-admin)])]
     {:db (assoc-in db [:db-access :checking-agent?] false)
      :fx [[:dispatch [:modal->open {:content [db-access-not-available-dialog/main
                                               {:error-message error-message
                                                :user-is-admin? is-admin?}]
                                     :maxWidth "446px"}]]]})))

;; Clear current database access session
(rf/reg-event-fx
 :db-access->clear-session
 (fn [{:keys [db]} [_]]
   (.removeItem js/localStorage db-access-constants/db-access-storage-key)
   {:db (assoc-in db [:db-access :current] nil)}))

;; Reopen main connect modal (used by draggable card expand)
(rf/reg-event-fx
 :db-access->reopen-connect-modal
 (fn [{:keys [db]} [_]]
   ;; Get connection from current session data
   (let [current-session (get-in db [:db-access :current])
         connection {:name (:id current-session)
                     :connection_name (:connection_name current-session)}]
     {:fx [[:dispatch [:modal->open {:content [db-access-main/main connection]
                                     :maxWidth "1100px"
                                     :custom-on-click-out db-access-main/minimize-modal}]]]})))

;; Check for active database access sessions on app initialization
(rf/reg-event-fx
 :db-access->check-active-sessions
 (fn [{:keys [db]} [_]]
   (let [stored-data (.getItem js/localStorage db-access-constants/db-access-storage-key)]
     (if stored-data
       (try
         (let [parsed-data (reader/read-string stored-data)]
           (if (db-access-constants/db-access-valid? parsed-data)
             ;; Found active session - load it and show draggable card
             {:db (assoc-in db [:db-access :current] parsed-data)
              :fx [[:dispatch-later {:ms 1000 ; Wait for app to be ready
                                     :dispatch [:db-access->show-active-session parsed-data]}]]}
             ;; Session expired, clean it up
             {:fx [[:dispatch [:db-access->cleanup-expired]]]}))
         (catch js/Error _
           ;; Invalid data, clean it up
           {:fx [[:dispatch [:db-access->cleanup-expired]]]}))
       ;; No stored session
       {}))))

;; Show draggable card for active session
(rf/reg-event-fx
 :db-access->show-active-session
 (fn [_ [_ session-data]]
   {:fx [[:dispatch [:draggable-card->open
                     {:component [db-access-main/minimize-modal-content session-data]
                      :on-click-expand (fn []
                                         (rf/dispatch [:draggable-card->close])
                                         (rf/dispatch [:db-access->reopen-connect-modal]))}]]]}))

;; Auto-cleanup expired session on app initialization
(rf/reg-event-fx
 :db-access->cleanup-all-expired
 (fn [_ [_]]
   (let [stored-data (.getItem js/localStorage db-access-constants/db-access-storage-key)]
     (when stored-data
       (try
         (let [parsed-data (reader/read-string stored-data)]
           (when-not (db-access-constants/db-access-valid? parsed-data)
             (.removeItem js/localStorage db-access-constants/db-access-storage-key)))
         (catch js/Error _
           ;; Invalid data, remove it
           (.removeItem js/localStorage db-access-constants/db-access-storage-key))))
     {})))

;; Subscriptions
(rf/reg-sub
 :db-access->requesting?
 (fn [db _]
   (get-in db [:db-access :requesting?] false)))

(rf/reg-sub
 :db-access->checking-agent?
 (fn [db _]
   (get-in db [:db-access :checking-agent?] false)))

(rf/reg-sub
 :db-access->current-session
 (fn [db _]
   (get-in db [:db-access :current])))

(rf/reg-sub
 :db-access->session-valid?
 (fn [db _]
   (let [current-session (get-in db [:db-access :current])]
     (db-access-constants/db-access-valid? current-session))))
