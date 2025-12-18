(ns webapp.connections.native-client-access.constants
  (:require [cljs.reader :as reader]))

;; Access duration options (in minutes)
(def access-duration-options
  [{:text "15 minutes" :value 15}
   {:text "30 minutes" :value 30}
   {:text "1 hour" :value 60}
   {:text "2 hours" :value 120}
   {:text "4 hours" :value 240}])

;; Convert minutes to seconds for API
(defn minutes->seconds [minutes]
  (* minutes 60))

;; localStorage key for native client access data (multiple sessions)
(def native-client-access-storage-key "hoop-native-client-access")

;; Check if native client access data is still valid
(defn native-client-access-valid? [native-client-access-data]
  (when native-client-access-data
    (let [expire-at (new js/Date (:expire_at native-client-access-data))
          now (new js/Date)]
      (> (.getTime expire-at) (.getTime now)))))

;; Normalize sessions to a map of connection names to session data
(defn normalize-sessions [parsed]
  (cond
    (and (map? parsed) (:connection_name parsed))
    {(:connection_name parsed) parsed}

    (map? parsed)
    parsed

    :else
    {}))

;; Get all sessions from localStorage
(defn get-all-sessions []
  (try
    (if-let [stored-data (.getItem js/localStorage native-client-access-storage-key)]
      (-> stored-data
          reader/read-string
          normalize-sessions)
      {})
    (catch js/Error _
      {})))

;; Save all sessions to localStorage
(defn save-all-sessions [sessions]
  (try
    (.setItem js/localStorage native-client-access-storage-key (pr-str sessions))
    (catch js/Error e
      (js/console.error "Failed to save sessions to localStorage:" e))))

;; Add or update session
(defn save-session [connection-name session-data]
  (let [all-sessions (get-all-sessions)
        updated-sessions (assoc all-sessions connection-name session-data)]
    (save-all-sessions updated-sessions)))

;; Remove session by connection name
(defn remove-session [connection-name]
  (let [all-sessions (get-all-sessions)
        updated-sessions (dissoc all-sessions connection-name)]
    (if (empty? updated-sessions)
      (.removeItem js/localStorage native-client-access-storage-key)
      (save-all-sessions updated-sessions))))

;; Remove all expired sessions
(defn cleanup-expired-sessions []
  (let [all-sessions (get-all-sessions)
        valid-sessions (into {} (filter (fn [[_ session]]
                                          (native-client-access-valid? session))
                                        all-sessions))]
    (if (empty? valid-sessions)
      (.removeItem js/localStorage native-client-access-storage-key)
      (save-all-sessions valid-sessions))
    valid-sessions))

;; Error messages for different user types
(def error-messages
  {:agent-offline
   {:admin "The Agent configured for this connection is not available at this moment. Please reach out to your organization admin to enable it before proceeding."
    :non-admin "The Agent configured for this connection is not available at this moment. Please reach out to your organization admin to enable it before proceeding."}

   :generic
   {:admin "This connection method is not available at this moment. Please reach out to your organization admin to enable this method."
    :non-admin "This connection method is not available at this moment. Please reach out to your organization admin to enable this method."}})
