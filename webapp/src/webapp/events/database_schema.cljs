(ns webapp.events.database-schema
  (:require [re-frame.core :as rf]))

(defn- process-tables [tables-data]
  (reduce (fn [acc schema]
            (let [schema-name (:name schema)
                  tables-list (:tables schema)
                  tables (reduce (fn [table-acc table-name]
                                   (assoc table-acc table-name {}))
                                 {}
                                 tables-list)]
              (assoc acc schema-name tables)))
          {}
          (:schemas tables-data)))

(defn- process-columns [columns-data]
  (reduce (fn [acc column]
            (assoc acc (:name column)
                   {(:type column)
                    {"nullable" (:nullable column)}}))
          {}
          (:columns columns-data)))

(rf/reg-event-fx
 :database-schema->clear-schema
 (fn [{:keys [db]} [_]]
   (.removeItem js/localStorage "selected-database")
   {:db (-> db
            (assoc-in [:database-schema :data] nil))}))

(rf/reg-event-db
 :database-schema->clear-connection-schema
 (fn [db [_ connection-name]]
   (-> db
       (update-in [:database-schema :data] dissoc connection-name))))

(rf/reg-event-fx
 :database-schema->handle-multi-database-schema
 (fn [{:keys [db]} [_ connection]]
   {:fx [[:dispatch [:database-schema->get-multi-databases connection]]]}))

(rf/reg-event-fx
 :database-schema->get-multi-databases
 (fn [{:keys [db]} [_ connection]]
   {:db (-> db
            (assoc-in [:database-schema :current-connection] (:connection-name connection))
            (assoc-in [:database-schema :data (:connection-name connection) :status] :loading))
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/connections/" (:connection-name connection) "/databases")
                             :on-success (fn [response]
                                           (rf/dispatch [:database-schema->set-multi-databases
                                                         connection
                                                         (:databases response)]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:database-schema->set-multi-databases-error connection error]))}]]]}))

(rf/reg-event-fx
 :database-schema->set-multi-databases-error
 (fn [{:keys [db]} [_ connection error]]
   {:db (-> db
            (assoc-in [:database-schema :data (:connection-name connection) :status] :error)
            (assoc-in [:database-schema :data (:connection-name connection) :error]
                      (or error "Failed to load databases")))
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to load databases"
                                     :details (or error "Failed to load databases")}]]]}))

(rf/reg-event-db
 :database-schema->set-multi-databases
 (fn [db [_ connection databases]]
   (-> db
       (assoc-in [:database-schema :data (:connection-name connection) :databases] databases)
       (assoc-in [:database-schema :data (:connection-name connection) :status] :success)
       (assoc-in [:database-schema :data (:connection-name connection) :empty?] (empty? databases)))))

;; Common events for all connection types
(rf/reg-event-fx
 :database-schema->set-schema-error-size-exceeded
 (fn [{:keys [db]} [_ connection error]]
   {:db (-> db
            ;; Mark the general status as error
            (assoc-in [:database-schema :data (:connection-name connection) :status] :error)
            ;; Mark the schema status as error
            (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :error)
            ;; Define the error message
            (assoc-in [:database-schema :data (:connection-name connection) :error]
                      (or error "Schema size too large to display.")))
    ;; Show error in snackbar
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to load database schema"
                                     :details (or error "Schema size too large to display.")}]]]}))

;; Events for loading tables (for single-database banks)
(rf/reg-event-fx
 :database-schema->handle-database-schema
 (fn [{:keys [db]} [_ connection]]
   {:db (-> db
            (assoc-in [:database-schema :current-connection] (:connection-name connection))
            (assoc-in [:database-schema :data (:connection-name connection) :status] :loading)
            (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :loading))
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/connections/" (:connection-name connection) "/tables")
                             :on-success (fn [response]
                                           (rf/dispatch [:database-schema->tables-loaded connection nil response]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:database-schema->set-schema-error-size-exceeded connection error]))}]]]}))

;; Events for loading tables directly for DynamoDB
(rf/reg-event-fx
 :database-schema->handle-dynamodb-schema
 (fn [{:keys [db]} [_ connection]]
   {:db (-> db
            (assoc-in [:database-schema :current-connection] (:connection-name connection))
            ;; Ensure the status remains loading during the search
            (assoc-in [:database-schema :data (:connection-name connection) :status] :loading)
            (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :loading))
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/connections/" (:connection-name connection) "/tables")
                             :on-success (fn [response]
                                           (rf/dispatch [:database-schema->dynamodb-tables-loaded connection response]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:database-schema->set-schema-error-size-exceeded connection error]))}]]]}))

;; Handle DynamoDB tables
(rf/reg-event-db
 :database-schema->dynamodb-tables-loaded
 (fn [db [_ connection response]]
   (let [tables (get-in response [:schemas 0 :tables] [])
         ;; We treat tables as databases for DynamoDB
         databases tables]
     (-> db
         (assoc-in [:database-schema :data (:connection-name connection) :status] :success)
         (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :success)
         (assoc-in [:database-schema :data (:connection-name connection) :type] "dynamodb")
         ;; Instead of populating schema-tree, we populate the list of databases
         (assoc-in [:database-schema :data (:connection-name connection) :databases] databases)
         (assoc-in [:database-schema :data (:connection-name connection) :empty?] (empty? databases))
         (assoc-in [:database-schema :data (:connection-name connection) :schema-tree] {})
         (assoc-in [:database-schema :data (:connection-name connection) :columns-cache] {})
         (assoc-in [:database-schema :data (:connection-name connection) :loading-columns] #{})))))

;; Events for loading log groups directly for CloudWatch
(rf/reg-event-db
 :database-schema->cloudwatch-database-selected
 (fn [db [_ connection database]]
   ;; For CloudWatch, selecting a log group doesn't require loading tables
   ;; Just mark as selected and ensure loading state is clean
   (-> db
       (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :success)
       ;; Cleanup any potential loading state (defensive programming)
       (update-in [:database-schema :data (:connection-name connection) :loading-databases]
                  (fn [databases] (disj (or databases #{}) database))))))

(rf/reg-event-fx
 :database-schema->handle-cloudwatch-schema
 (fn [{:keys [db]} [_ connection]]
   {:db (-> db
            (assoc-in [:database-schema :current-connection] (:connection-name connection))
            ;; Ensure the status remains loading during the search
            (assoc-in [:database-schema :data (:connection-name connection) :status] :loading)
            (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :loading))
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/connections/" (:connection-name connection) "/tables")
                             :on-success (fn [response]
                                           (rf/dispatch [:database-schema->cloudwatch-tables-loaded connection response]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:database-schema->set-schema-error-size-exceeded connection error]))}]]]}))

;; Handle CloudWatch log groups
(rf/reg-event-db
 :database-schema->cloudwatch-tables-loaded
 (fn [db [_ connection response]]
   (let [tables (get-in response [:schemas 0 :tables] [])
         ;; We treat log groups as databases for CloudWatch
         databases tables]
     (-> db
         (assoc-in [:database-schema :data (:connection-name connection) :status] :success)
         (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :success)
         (assoc-in [:database-schema :data (:connection-name connection) :type] "cloudwatch")
         ;; Instead of populating schema-tree, we populate the list of databases
         (assoc-in [:database-schema :data (:connection-name connection) :databases] databases)
         (assoc-in [:database-schema :data (:connection-name connection) :empty?] (empty? databases))
         (assoc-in [:database-schema :data (:connection-name connection) :schema-tree] {})
         (assoc-in [:database-schema :data (:connection-name connection) :columns-cache] {})
         (assoc-in [:database-schema :data (:connection-name connection) :loading-columns] #{})))))

;; Events for loading tables (for multi-database banks)
(rf/reg-event-fx
 :database-schema->load-tables
 (fn [{:keys [db]} [_ connection database]]
   {:db (-> db
            (assoc-in [:database-schema :current-connection] (:connection-name connection))
            (assoc-in [:database-schema :data (:connection-name connection) :status] :loading)
            (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :loading)
            (assoc-in [:database-schema :data (:connection-name connection) :current-database] database))
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/connections/" (:connection-name connection) "/tables?database=" database)
                             :on-success (fn [response]
                                           (rf/dispatch [:database-schema->tables-loaded connection database response]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:database-schema->set-schema-error-size-exceeded connection error]))}]]]}))

(rf/reg-event-db
 :database-schema->tables-loaded
 (fn [db [_ connection database response]]
   (let [open-db (or database
                     (get-in db [:database-schema :data (:connection-name connection) :current-database]))
         schema-tree (process-tables response)
         is-empty? (empty? schema-tree)]
     (-> db
         (assoc-in [:database-schema :data (:connection-name connection) :status] :success)
         (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :success)
         (assoc-in [:database-schema :data (:connection-name connection) :type] (:subtype connection))
         (assoc-in [:database-schema :data (:connection-name connection) :current-database] open-db)
         (assoc-in [:database-schema :data (:connection-name connection) :open-database] open-db)
         (assoc-in [:database-schema :data (:connection-name connection) :schema-tree] schema-tree)
         (assoc-in [:database-schema :data (:connection-name connection) :empty?] is-empty?)
         (assoc-in [:database-schema :data (:connection-name connection) :columns-cache] {})
         (assoc-in [:database-schema :data (:connection-name connection) :loading-columns] #{})

         (update-in [:database-schema :data (:connection-name connection) :loading-databases]
                    (fn [databases] (disj (or databases #{}) database)))))))

;; Events for progressive loading of columns
(rf/reg-event-fx
 :database-schema->load-columns
 (fn [{:keys [db]} [_ connection-name database table-name schema-name]]
   (let [cache-key (str schema-name "." table-name)
         uri (if database
               ;; If there is a database, include it in the URI
               (str "/connections/" connection-name
                    "/columns?database=" database
                    "&table=" table-name
                    "&schema=" schema-name)
               ;; Otherwise, do not include database in the URI
               (str "/connections/" connection-name
                    "/columns?table=" table-name
                    "&schema=" schema-name))]

     (if (get-in db [:database-schema :data connection-name :columns-cache cache-key])
       {}

       {:db (update-in db [:database-schema :data connection-name :loading-columns] conj cache-key)
        :fx [[:dispatch [:fetch {:method "GET"
                                 :uri uri
                                 :on-success (fn [response]
                                               (rf/dispatch [:database-schema->columns-loaded
                                                             connection-name database schema-name table-name response]))
                                 :on-failure (fn [error]
                                               (rf/dispatch [:database-schema->columns-failure
                                                             connection-name schema-name table-name error]))}]]]}))))

(rf/reg-event-db
 :database-schema->columns-loaded
 (fn [db [_ connection-name database schema-name table-name response]]
   (let [cache-key (str schema-name "." table-name)
         columns-map (process-columns response)]
     (-> db
         (update-in [:database-schema :data connection-name :loading-columns] disj cache-key)
         (assoc-in [:database-schema :data connection-name :columns-cache cache-key] columns-map)
         (assoc-in [:database-schema :data connection-name :schema-tree schema-name table-name] columns-map)))))

(rf/reg-event-fx
 :database-schema->columns-failure
 (fn [{:keys [db]} [_ connection-name schema-name table-name error]]
   (let [cache-key (str schema-name "." table-name)]
     {:db (-> db
              (update-in [:database-schema :data connection-name :loading-columns] disj cache-key)
              (assoc-in [:database-schema :data connection-name :columns-cache cache-key]
                        {:error (or (.-message error) "Failed to load columns")}))
      :fx [[:dispatch [:show-snackbar {:level :error
                                       :text (str "Failed to load columns for " table-name)
                                       :details (or (.-message error) "Failed to load columns")}]]]})))

(rf/reg-event-fx
 :database-schema->change-database
 (fn [{:keys [db]} [_ connection database]]
   (let [current-db (get-in db [:database-schema :data (:connection-name connection) :current-database])
         open-db (get-in db [:database-schema :data (:connection-name connection) :open-database])
         loading-databases (get-in db [:database-schema :data (:connection-name connection) :loading-databases] #{})
         already-loading? (contains? loading-databases database)
         connection-type (get-in db [:database-schema :data (:connection-name connection) :type])]

     (.setItem js/localStorage "selected-database" database)

     ;; If the database is already loading or if it's the current database and it's already open, do nothing
     (if (or already-loading?
             (and (= database current-db)
                  (= database open-db)))
       {}
       ;; Otherwise, start loading or reopen
       {:db (-> db
                (assoc-in [:database-schema :data (:connection-name connection) :open-database] database)
                (assoc-in [:database-schema :data (:connection-name connection) :current-database] database)
                ;; Only set loading status if it's not the current database (otherwise we already have the data)
                (cond-> (not= database current-db)
                  ;; Update status to loading
                  (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :loading))
                (cond-> (and (not= database current-db)
                             (not= connection-type "cloudwatch"))
                  ;; Add to the list of databases in loading (except CloudWatch)
                  (update-in [:database-schema :data (:connection-name connection) :loading-databases]
                             (fn [databases] (conj (or databases #{}) database)))))
        ;; Only dispatch loading if it's not the current database
        :fx (when (not= database current-db)
              (cond
                (= connection-type "dynamodb")
                ;; For DynamoDB, we load the table columns directly
                [[:dispatch [:database-schema->load-dynamodb-table connection database]]]

                (= connection-type "cloudwatch")
                ;; For CloudWatch, log groups don't have columns, just select and immediately finish loading
                [[:dispatch [:database-schema->cloudwatch-database-selected connection database]]]

                :else
                ;; For other databases, we use the existing load-tables event
                [[:dispatch [:database-schema->load-tables connection database]]]))}))))

;; Event to close the selected database
(rf/reg-event-db
 :database-schema->close-database
 (fn [db [_ connection]]
   (-> db
       ;; Only clear the open-database, keeping the current-database for cache
       (assoc-in [:database-schema :data (:connection-name connection) :open-database] nil))))

;; Event to set loading status
(rf/reg-event-db
 :database-schema->set-loading-status
 (fn [db [_ connection]]
   (-> db
       (assoc-in [:database-schema :current-connection] (:connection-name connection))
       (assoc-in [:database-schema :data (:connection-name connection) :status] :loading))))

;; Smart event to ensure schema is loaded (replaces complex component logic)
(rf/reg-event-fx
 :database-schema->ensure-loaded
 (fn [{:keys [db]} [_ connection]]
   (let [connection-name (:connection-name connection)
         connection-type (:connection-type connection)
         current-connection (get-in db [:database-schema :current-connection])
         current-schema (get-in db [:database-schema :data connection-name])
         should-load? (or
                       ;; Connection changed
                       (not= current-connection connection-name)
                       ;; No schema exists
                       (not current-schema))]

     (if should-load?
       ;; Load schema
       {:db (assoc-in db [:database-schema :current-connection] connection-name)
        :fx [(case connection-type
               "oracledb" [:dispatch [:database-schema->handle-database-schema connection]]
               "mssql" [:dispatch [:database-schema->handle-database-schema connection]]
               "postgres" [:dispatch [:database-schema->handle-multi-database-schema connection]]
               "mysql" [:dispatch [:database-schema->handle-multi-database-schema connection]]
               "mongodb" [:dispatch [:database-schema->handle-multi-database-schema connection]]
               "dynamodb" [:dispatch [:database-schema->handle-dynamodb-schema connection]]
               "cloudwatch" [:dispatch [:database-schema->handle-cloudwatch-schema connection]]
               ;; Default: do nothing for unknown types
               [:dispatch [:database-schema->set-loading-status connection]])]}

       ;; Auto-select persisted database if needed
       (let [selected-db (.getItem js/localStorage "selected-database")
             postgres-or-mongo? (contains? #{"postgres" "mongodb"} connection-type)
             has-success? (= (:status current-schema) :success)
             no-open-db? (not (get-in current-schema [:open-database]))]
         (if (and postgres-or-mongo? selected-db has-success? no-open-db?)
           {:fx [[:dispatch [:database-schema->change-database
                             {:connection-name connection-name}
                             selected-db]]]}
           ;; No action needed
           {}))))))

;; Events to load specific DynamoDB tables when the user selects a "database"
(rf/reg-event-fx
 :database-schema->load-dynamodb-table
 (fn [{:keys [db]} [_ connection table-name]]
   {:db (-> db
            (assoc-in [:database-schema :current-connection] (:connection-name connection))
            (assoc-in [:database-schema :data (:connection-name connection) :status] :loading)
            ;; Use database-schema-status instead of table-columns-status to maintain consistency
            (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :loading)
            (assoc-in [:database-schema :data (:connection-name connection) :current-table] table-name)
            ;; Add to the list of databases in loading for visual control
            (update-in [:database-schema :data (:connection-name connection) :loading-databases]
                       (fn [databases] (conj (or databases #{}) table-name))))
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/connections/" (:connection-name connection) "/columns?table=" table-name)
                             :on-success (fn [response]
                                           (rf/dispatch [:database-schema->dynamodb-columns-loaded connection table-name response]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:database-schema->set-schema-error-size-exceeded connection error]))}]]]}))

;; Handler to process DynamoDB columns
(rf/reg-event-db
 :database-schema->dynamodb-columns-loaded
 (fn [db [_ connection table-name response]]
   (let [columns-map (process-columns response)]
     (-> db
         (assoc-in [:database-schema :data (:connection-name connection) :status] :success)
         ;; Use database-schema-status instead of table-columns-status
         (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :success)
         (assoc-in [:database-schema :data (:connection-name connection) :current-table] table-name)
         (assoc-in [:database-schema :data (:connection-name connection) :columns-cache table-name] columns-map)
         (assoc-in [:database-schema :data (:connection-name connection) :schema-tree "default" table-name] columns-map)
         ;; Remove from the list of databases in loading
         (update-in [:database-schema :data (:connection-name connection) :loading-databases]
                    (fn [databases] (disj (or databases #{}) table-name)))))))
