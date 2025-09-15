(ns webapp.events.db-access
  (:require
   [cljs.reader :as reader]
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.connections.constants.db-access :as db-access-constants]
   [webapp.connections.views.db-access-connect-dialog :as db-access-connect-dialog]
   [webapp.connections.views.db-access-duration-dialog :as db-access-duration-dialog]
   [webapp.connections.views.db-access-not-available-dialog :as db-access-not-available-dialog]))

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
   (let [connection-id (:id response)
         storage-key (db-access-constants/db-access-storage-key connection-id)]

     ;; Save to localStorage
     (.setItem js/localStorage storage-key (pr-str response))

     {:db (-> db
              (assoc-in [:db-access :requesting?] false)
              (assoc-in [:db-access :current] response))
      :fx [[:dispatch [:show-snackbar {:level :success
                                       :text "Database access granted successfully!"}]]
           [:dispatch [:modal->open {:content [db-access-connect-dialog/main]
                                     :maxWidth "600px"
                                     :custom-on-click-out db-access-connect-dialog/minimize-modal}]]]})))

;; Handle failed database access response
(rf/reg-event-fx
 :db-access->request-failure
 (fn [{:keys [db]} [_ error]]
   (let [is-admin? (get-in db [:users->current-user :data :admin?])
         error-message (or (:message error)
                           (get-in error [:response :message])
                           "Failed to request database access")]
     {:db (assoc-in db [:db-access :requesting?] false)
      :fx [[:dispatch [:modal->open {:content [db-access-not-available-dialog/main
                                               {:error-message error-message
                                                :user-is-admin? is-admin?}]
                                     :maxWidth "446px"}]]]})))

;; Load database access from localStorage
(rf/reg-event-fx
 :db-access->load-from-storage
 (fn [{:keys [db]} [_ connection-id]]
   (let [storage-key (db-access-constants/db-access-storage-key connection-id)
         stored-data (.getItem js/localStorage storage-key)]
     (if stored-data
       (try
         (let [parsed-data (reader/read-string stored-data)]
           (if (db-access-constants/db-access-valid? parsed-data)
             {:db (assoc-in db [:db-access :current] parsed-data)}
             ;; Data expired, clean it up
             {:fx [[:dispatch [:db-access->cleanup-expired connection-id]]]}))
         (catch js/Error _
           ;; Invalid data, clean it up
           {:fx [[:dispatch [:db-access->cleanup-expired connection-id]]]}))
       {}))))

;; Clean up expired or invalid database access data
(rf/reg-event-fx
 :db-access->cleanup-expired
 (fn [{:keys [db]} [_ connection-id]]
   (let [storage-key (db-access-constants/db-access-storage-key connection-id)]
     (.removeItem js/localStorage storage-key)
     {:db (assoc-in db [:db-access :current] nil)})))

;; Start database access flow - go directly to duration selection
(rf/reg-event-fx
 :db-access->start-flow
 (fn [_ [_ connection]]
   ;; Backend will handle all validations (proxy port, review, etc.)
   ;; Frontend just presents the UI and handles responses
   {:fx [[:dispatch [:modal->open {:content [db-access-duration-dialog/main connection]
                                   :maxWidth "446px"}]]]}))

;; Clear current database access session
(rf/reg-event-fx
 :db-access->clear-session
 (fn [{:keys [db]} [_]]
   (let [current-session (get-in db [:db-access :current])
         connection-id (:id current-session)]
     (when connection-id
       (let [storage-key (db-access-constants/db-access-storage-key connection-id)]
         (.removeItem js/localStorage storage-key)))
     {:db (assoc-in db [:db-access :current] nil)})))

;; Reopen main connect modal (used by draggable card expand)
(rf/reg-event-fx
 :db-access->reopen-connect-modal
 (fn [_ [_]]
   {:fx [[:dispatch [:modal->open {:content [db-access-connect-dialog/main]
                                   :maxWidth "600px"
                                   :custom-on-click-out db-access-connect-dialog/minimize-modal}]]]}))

;; Auto-cleanup expired sessions on app initialization
(rf/reg-event-fx
 :db-access->cleanup-all-expired
 (fn [_ [_]]
   (let [local-storage-keys (for [i (range (.-length js/localStorage))]
                              (.key js/localStorage i))
         db-access-keys (filter #(and % (str/starts-with? % "hoop-db-access-"))
                                local-storage-keys)]

     ;; Check each stored session and remove expired ones
     (doseq [storage-key db-access-keys]
       (try
         (let [stored-data (.getItem js/localStorage storage-key)]
           (when stored-data
             (let [parsed-data (reader/read-string stored-data)]
               (when-not (db-access-constants/db-access-valid? parsed-data)
                 (.removeItem js/localStorage storage-key)))))
         (catch js/Error _
           ;; Invalid data, remove it
           (.removeItem js/localStorage storage-key))))

     {})))

;; Subscriptions
(rf/reg-sub
 :db-access->requesting?
 (fn [db _]
   (get-in db [:db-access :requesting?] false)))

(rf/reg-sub
 :db-access->current-session
 (fn [db _]
   (get-in db [:db-access :current])))

(rf/reg-sub
 :db-access->session-valid?
 (fn [db _]
   (let [current-session (get-in db [:db-access :current])]
     (db-access-constants/db-access-valid? current-session))))
