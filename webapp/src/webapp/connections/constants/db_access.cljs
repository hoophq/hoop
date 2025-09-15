(ns webapp.connections.constants.db-access)

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

;; Get default access duration (30 minutes)
(def default-access-duration 30)

;; localStorage key for db access data
(defn db-access-storage-key [connection-id]
  (str "hoop-db-access-" connection-id))

;; Check if db access data is still valid
(defn db-access-valid? [db-access-data]
  (when db-access-data
    (let [expire-at (new js/Date (:expire_at db-access-data))
          now (new js/Date)
          _ (println (.getTime expire-at) (.getTime now))]
      (> (.getTime expire-at) (.getTime now)))))

;; Error messages for different user types
(def error-messages
  {:review-active
   {:admin "This connection has review enabled. Database access is not available for connections requiring review approval."
    :non-admin "This connection requires review approval and cannot be accessed directly. Please contact your administrator."}

   :proxy-port-missing
   {:admin "Database proxy port is not configured. Please configure the PostgreSQL server settings in Infrastructure > Configuration."
    :non-admin "Database access is currently unavailable. Please contact your administrator to configure the necessary settings."}})
