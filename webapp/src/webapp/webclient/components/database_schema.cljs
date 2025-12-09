(ns webapp.webclient.components.database-schema
  (:require ["@radix-ui/themes" :refer [Box Text Spinner]]
            ["lucide-react" :refer [ChevronDown ChevronRight Database File
                                    FolderClosed FolderOpen Table]]
            [reagent.core :as r]
            [re-frame.core :as rf]
            [webapp.subs :as subs]))

;; Adding memoization for components that are rendered frequently
(def memoized-field-type-tree
  (memoize
   (fn [type]
     [:div {:class "text-xs pl-regular italic"}
      (str "(" type ")")])))

(defn- field-type-tree [type]
  (memoized-field-type-tree type))

(defn- loading-indicator [message]

  [:div {:class "flex gap-small items-center pb-small ml-small text-xs"}
   [:span {:class "italic"} message]
   [:> Spinner {:size "1" :color "gray"}]])

(defn- empty-state [connection-type & {:keys [for-database?] :or {for-database? false}}]
  (let [message (if for-database?
                  ;; Empty state for a specific database (no schemas/tables)
                  (case connection-type
                    "postgres" "No schemas found"
                    "mysql" "No schemas found"
                    "mongodb" "No collections found"
                    "oracledb" "No schemas found"
                    "mssql" "No schemas found"
                    "dynamodb" "No columns found"
                    "cloudwatch" "No log streams found"
                    "No data found")
                  ;; Empty state for the database list (no databases)
                  (case connection-type
                    "postgres" "No databases found"
                    "mysql" "No databases found"
                    "mongodb" "No databases found"
                    "oracledb" "No schemas found"
                    "mssql" "No databases found"
                    "dynamodb" "No tables found"
                    "cloudwatch" "No log groups found"
                    "No data found"))
        detail-text (if for-database?
                      ;; Detail for specific database
                      (case connection-type
                        "postgres" "schemas"
                        "mysql" "schemas"
                        "mongodb" "collections"
                        "oracledb" "schemas"
                        "mssql" "schemas"
                        "dynamodb" "columns"
                        "cloudwatch" "log streams"
                        "data")
                      ;; Detail for database list
                      (case connection-type
                        "postgres" "databases"
                        "mysql" "databases"
                        "mongodb" "databases"
                        "oracledb" "schemas"
                        "mssql" "databases"
                        "dynamodb" "tables"
                        "cloudwatch" "log groups"
                        "data"))
        context-text (if for-database?
                       " for this database."
                       " for this resource role.")]
    (if (not for-database?)
      [:div {:class "flex flex-col items-center justify-center py-8 text-center"}
       [:> Text {:size "2" :mb "2" :weight "medium"} message]
       [:> Text {:size "1" :weight "medium"}
        "We couldn't find any "
        detail-text
        context-text]]

      [:div {:class "flex flex-col pt-1 pb-2 px-2"}
       [:> Text {:size "1" :weight "bold"} message]
       [:> Text {:size "1" :weight "light"}
        "We couldn't find any "
        detail-text
        context-text]])))

(defn- fields-tree [fields]
  (let [dropdown-status (r/atom {})
        dropdown-columns-status (r/atom :closed)]
    (fn []
      (let [current-status @dropdown-status
            current-columns-status @dropdown-columns-status]
        [:div {:class "pl-small"}
         [:div {:class "flex items-center gap-small mb-2"}
          (if (= current-columns-status :closed)
            [:> FolderClosed {:size 12}]
            [:> FolderOpen {:size 12}])
          [:> Text {:size "1" :weight "medium"
                    :class "hover:underline cursor-pointer flex items-center"
                    :on-click #(reset! dropdown-columns-status
                                       (if (= current-columns-status :open) :closed :open))}
           "Columns"
           (if (= current-columns-status :open)
             [:> ChevronDown {:size 12}]
             [:> ChevronRight {:size 12}])]]

         (when (= current-columns-status :open)
           [:div {:class "pl-small"}
            (for [[field field-type] fields]
              ^{:key field}
              [:div
               [:div {:class "flex items-center gap-small mb-2"}
                [:> File {:size 12}]
                [:span {:class "hover:text-blue-500 hover:underline cursor-pointer flex items-center"
                        :on-click #(swap! dropdown-status
                                          assoc-in [field]
                                          (if (= (get current-status field) :open) :closed :open))}
                 [:> Text {:size "1" :weight "medium"} field]
                 (if (= (get current-status field) :open)
                   [:> ChevronDown {:size 12}]
                   [:> ChevronRight {:size 12}])]]

               (when (= (get current-status field) :open)
                 [field-type-tree (first (map key field-type))])])])]))))

(defn- tables-tree []
  (let [dropdown-status (r/atom {})]
    (fn [tables connection-name schema-name current-database loading-columns columns-cache]
      (let [current-status @dropdown-status]
        [:div {:class "pl-small"}
         (doall
          (for [[table fields] tables]
            (let [cache-key (str schema-name "." table)
                  is-loading (contains? loading-columns cache-key)
                  has-columns (or (seq fields) (contains? columns-cache cache-key))]
              ^{:key table}
              [:div
               [:div {:class "flex items-center gap-small mb-2"}
                [:> Box
                 [:> Table {:size 12}]]
                [:span {:class (str "hover:text-blue-500 hover:underline cursor-pointer "
                                    (when is-loading "opacity-50 ")
                                    "flex items-center")
                        :on-click #(do
                                     (swap! dropdown-status
                                            assoc-in [table]
                                            (if (= (get current-status table) :open) :closed :open))
                                     (when (and (not has-columns)
                                                (not= (get current-status table) :open)
                                                (not is-loading))
                                       (rf/dispatch [:database-schema->load-columns
                                                     connection-name
                                                     current-database
                                                     table
                                                     schema-name])))}
                 [:> Text {:size "1" :weight "medium"} table]
                 (if (= (get current-status table) :open)
                   [:> ChevronDown {:size 12}]
                   [:> ChevronRight {:size 12}])]]

               (when (= (get current-status table) :open)
                 [:div
                  (cond
                    is-loading
                    [loading-indicator "Loading columns..."]

                    (and (contains? columns-cache cache-key)
                         (contains? (get columns-cache cache-key) :error))
                    [:> Text {:as "p" :size "1" :mb "2" :ml "2" :color "red"}
                     (get-in columns-cache [cache-key :error])]

                    (contains? columns-cache cache-key)
                    [fields-tree (get columns-cache cache-key)]

                    :else
                    [fields-tree fields])])])))]))))

(defn- schema-view []
  (let [dropdown-status (r/atom nil)
        initialized (r/atom false)]
    (fn [schema-name tables connection-name current-schema _database-schema-status & {:keys [is-first] :or {is-first false}}]
      (when (not @initialized)
        (reset! dropdown-status (if is-first :open :closed))
        (reset! initialized true))

      (let [current-database (get-in current-schema [:current-database])
            loading-columns (get-in current-schema [:loading-columns] #{})
            columns-cache (get-in current-schema [:columns-cache] {})]
        [:div
         [:div {:class "flex items-center gap-small mb-2"}
          [:> Database {:size 12}]
          [:span {:class "hover:text-blue-500 hover:underline cursor-pointer flex items-center"
                  :on-click #(reset! dropdown-status (if (= @dropdown-status :open) :closed :open))}
           [:> Text {:size "1" :weight "medium"} schema-name]
           (if (= @dropdown-status :open)
             [:> ChevronDown {:size 12}]
             [:> ChevronRight {:size 12}])]]
         (when (= @dropdown-status :open)
           [tables-tree (into (sorted-map) tables)
            connection-name
            schema-name
            current-database
            loading-columns
            columns-cache])]))))

(defn- database-item [db connection-name database-schema-status current-schema type]
  (let [is-selected (= db (get-in current-schema [:open-database]))
        current-database (get-in current-schema [:current-database])
        loading-databases (get-in current-schema [:loading-databases] #{})
        is-loading-this-db (contains? loading-databases db)
        ;; For the selected database, use the schema-tree (which contains the loaded schemas)
        ;; For other databases, use empty schema (they haven't been loaded yet)
        db-schemas (if (and is-selected (= db current-database))
                     (or (not-empty (get-in current-schema [:schema-tree])) {})
                     {})
        database-errors (get-in current-schema [:database-errors] {})
        db-error (get database-errors db)]
    [:div
     [:div {:class "flex items-center gap-smal mb-2"}
      [:span {:class (str "hover:text-blue-500 hover:underline cursor-pointer "
                          (when is-loading-this-db "opacity-75 ")
                          "flex items-center")
              :on-click #(if (= type "cloudwatch")
                           ;; CloudWatch: simple selection without expand/collapse
                           (do
                             (.setItem js/localStorage "selected-database" db)
                             (rf/dispatch [:database-schema->change-database
                                           {:connection-name connection-name}
                                           db]))
                           ;; Other types: normal expand/collapse behavior
                           (if is-selected
                             (rf/dispatch [:database-schema->close-database {:connection-name connection-name}])
                             (rf/dispatch [:database-schema->change-database
                                           {:connection-name connection-name}
                                           db])))}
       [:> Text {:size "1" :weight "bold"} db]
       ;; CloudWatch doesn't show expand/collapse icons
       (when (not= type "cloudwatch")
         (if is-selected
           [:> ChevronDown {:size 12}]
           [:> ChevronRight {:size 12}]))]]

     (when is-selected
       [:div
        (cond
          is-loading-this-db
          [loading-indicator (cond
                               (= type "dynamodb") "Loading columns..."
                               (= type "cloudwatch") "Selecting log group..."
                               :else "Loading tables...")]

          ;; Check for database-specific error first
          db-error
          [:> Text {:as "p" :weight "medium" :size "1" :mb "2" :ml "2" :color "red"}
           db-error]

          (= "mysql" type)
          (let [schema-name (first (keys db-schemas))
                tables (first (vals db-schemas))
                current-database (get-in current-schema [:current-database])
                loading-columns (get-in current-schema [:loading-columns] #{})
                columns-cache (get-in current-schema [:columns-cache] {})]
            [:div
             [tables-tree (into (sorted-map) tables)
              connection-name
              schema-name
              current-database
              loading-columns
              columns-cache]])

          ;; Special case for CloudWatch - just show selection message
          (= type "cloudwatch")
          [:div
           [:> Text {:as "p" :weight "medium" :size "1" :mb "2" :ml "2"}
            (str "✓ Selected log group: " db)]]

          ;; Special case for DynamoDB when columns were loaded
          (and (= type "dynamodb") (get-in current-schema [:columns-cache db]))
          (let [columns-map (get-in current-schema [:columns-cache db])]
            [:div
             [fields-tree columns-map]])

          (not-empty db-schemas)
          [:div
           (doall
            (map-indexed
             (fn [idx [schema-name tables]]
               ^{:key schema-name}
               [schema-view
                schema-name
                tables
                connection-name
                current-schema
                database-schema-status
                :is-first (= idx 0)])
             db-schemas))]

          :else
          [empty-state type :for-database? true])])]))

(defn- databases-tree []
  (fn [databases _schema connection-name database-schema-status current-schema type]
    [:div.text-xs
     (doall
      (for [db databases]
        ^{:key db}
        [database-item
         db
         connection-name
         database-schema-status
         current-schema
         type]))]))

(defn- sql-databases-tree []
  (fn [schema connection-name current-schema database-schema-status]
    [:div
     (cond
       (and (= :error database-schema-status) (:error current-schema))
       [:> Text {:as "p" :size "1" :mb "2" :ml "2"}
        (:error current-schema)]

       (and (= :success database-schema-status) (empty? schema))
       (let [connection-type (get current-schema :type)]
         [empty-state connection-type])

       :else
       (doall
        (map-indexed
         (fn [idx [schema-name tables]]
           ^{:key schema-name}
           [schema-view
            schema-name
            tables
            connection-name
            current-schema
            database-schema-status
            :is-first (= idx 0)])
         schema)))]))

(defn db-view [{:keys [type schema databases connection-name current-schema database-schema-status]}]
  (let [is-empty? (get current-schema :empty?)
        ;; For multi-database types, empty? means no databases in the list
        ;; For single-database types (oracledb), empty? means no schemas
        is-multi-db-type? (contains? #{"mssql" "mysql" "postgres" "mongodb" "dynamodb" "cloudwatch"} type)]
    (cond
      ;; Only show global empty state for multi-db types when there are no databases at all
      (and is-multi-db-type? (= database-schema-status :success) is-empty? (empty? databases))
      [empty-state type]

      ;; For single-db types (oracledb), show empty state if no schemas
      (and (not is-multi-db-type?) (= database-schema-status :success) is-empty?)
      [empty-state type]

      :else
      (case type
        "oracledb" [sql-databases-tree (into (sorted-map) schema) connection-name current-schema database-schema-status]

        "mssql" [databases-tree databases (into (sorted-map) schema) connection-name database-schema-status current-schema type]
        "mysql" [databases-tree databases (into (sorted-map) schema) connection-name database-schema-status current-schema type]
        "postgres" [databases-tree databases (into (sorted-map) schema) connection-name database-schema-status current-schema type]
        "mongodb" [databases-tree databases (into (sorted-map) schema) connection-name database-schema-status current-schema type]

        ;; Modified for DynamoDB - use databases-tree as multi-database banks
        "dynamodb" [databases-tree databases (into (sorted-map) schema) connection-name database-schema-status current-schema type]

        ;; CloudWatch - use databases-tree for log groups (similar to DynamoDB)
        "cloudwatch" [databases-tree databases (into (sorted-map) schema) connection-name database-schema-status current-schema type]

        [:> Text {:size "1"}
         "Couldn't load the schema"]))))

(defn tree-view-status [{:keys [status
                                databases
                                schema
                                connection
                                current-schema
                                database-schema-status]}]
  [:> Box {:class "text-gray-12"}
   (cond
     (and (= status :loading) (empty? schema) (empty? databases))
     [loading-indicator "Loading schema"]

     ;; Only show global error if it's not a database-specific error
     ;; Database-specific errors are shown in database-item component
     (and (or (= status :failure) (= status :error) (= database-schema-status :error))
          (empty? (get-in current-schema [:database-errors] {}))
          (:error current-schema))
     [:div {:class "flex gap-small items-center py-regular text-xs text-red-500"}
      [:span
       (or (some-> current-schema :error :message)
           "Failed to load database schema")]]

     :else [db-view {:type (:connection-type connection)
                     :schema schema
                     :databases databases
                     :connection-name (:connection-name connection)
                     :database-schema-status database-schema-status
                     :current-schema current-schema}])])

(defn main []
  (let [database-schema (rf/subscribe [::subs/database-schema])]
    (fn [connection]
      (let [connection-name (:connection-name connection)
            current-schema (get-in @database-schema [:data connection-name])]

        (when (and connection connection-name)
          (rf/dispatch [:database-schema->ensure-loaded connection]))

        [tree-view-status
         {:status (:status current-schema)
          :databases (:databases current-schema)
          :schema (:schema-tree current-schema)
          :connection connection
          :database-schema-status (:database-schema-status current-schema)
          :current-schema current-schema}]))))
