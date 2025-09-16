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

;; localStorage key for db access data
(defn db-access-storage-key [connection-id]
  (str "hoop-db-access-" connection-id))

;; Get text label for a given duration value
(defn get-duration-text [value]
  (some-> (filter #(= (:value %) value) access-duration-options)
          first
          :text))

;; Alternative using some (more performant)
(defn get-duration-text-alt [value]
  (some #(when (= (:value %) value) (:text %)) access-duration-options))

;; Lookup map for O(1) access (best for frequent usage)
(def duration-value->text
  (->> access-duration-options
       (map (juxt :value :text))
       (into {})))

;; Fast lookup function using the map
(defn get-duration-text-fast [value]
  (get duration-value->text value))

;; Examples of usage:
;; (get-duration-text 60)       => "1 hour"
;; (get-duration-text-alt 120)  => "2 hours"
;; (get-duration-text-fast 15)  => "15 minutes"
;; (get-duration-text 999)      => nil (value not found)

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
