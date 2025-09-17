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

;; localStorage key for db access data (single session)
(def db-access-storage-key "hoop-db-access")

;; Check if db access data is still valid
(defn db-access-valid? [db-access-data]
  (when db-access-data
    (let [expire-at (new js/Date (:expire_at db-access-data))
          now (new js/Date)
          _ (println (.getTime expire-at) (.getTime now))]
      (> (.getTime expire-at) (.getTime now)))))

;; Error messages for different user types
(def error-messages
  {:agent-offline
   {:admin "The Agent configured for this connection is not available at this moment. Please reach out to your organization admin to enable it before proceeding."
    :non-admin "The Agent configured for this connection is not available at this moment. Please reach out to your organization admin to enable it before proceeding."}

   :generic
   {:admin "This connection method is not available at this moment. Please reach out to your organization admin to enable this method."
    :non-admin "This connection method is not available at this moment. Please reach out to your organization admin to enable this method."}})
