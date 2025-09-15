(ns webapp.connections.helpers.db-access-validation
  (:require
   [clojure.string :as str]
   [webapp.connections.constants.db-access :as db-access-constants]))

(defn proxy-port-configured?
  "Check if PostgreSQL proxy port is configured in infrastructure"
  [infrastructure-config]
  (let [postgres-config (:postgres_server_config infrastructure-config)
        listen-address (:listen_address postgres-config)]
    (and listen-address
         (not (str/blank? listen-address)))))

(defn validate-db-access-eligibility
  "Validate if connection is eligible for database access
   Returns {:valid? boolean :error-type keyword :error-message string}"
  [connection infrastructure-config user-is-admin?]

  (cond
    ;; Check if connection type is supported
    (not (= (:subtype connection) "postgres"))
    {:valid? false
     :error-type :unsupported-type
     :error-message "Database access is only available for PostgreSQL connections."}

    ;; Check if review is active
    (:has_review connection)
    {:valid? false
     :error-type :review-active
     :error-message (get-in db-access-constants/error-messages
                            [:review-active (if user-is-admin? :admin :non-admin)])}

    ;; Check if proxy port is configured
    (not (proxy-port-configured? infrastructure-config))
    {:valid? false
     :error-type :proxy-port-missing
     :error-message (get-in db-access-constants/error-messages
                            [:proxy-port-missing (if user-is-admin? :admin :non-admin)])}

    ;; All validations passed
    :else
    {:valid? true}))
