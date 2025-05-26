(ns webapp.webclient.components.database-schema
  (:require ["@radix-ui/themes" :refer [Text]]
            ["lucide-react" :refer [ChevronDown ChevronRight Database File FolderClosed FolderOpen Table]]
            [reagent.core :as r]
            [re-frame.core :as rf]
            [webapp.subs :as subs]
            [webapp.config :as config]))

(defmulti get-database-schema identity)
(defmethod get-database-schema "oracledb" [_ connection]
  (rf/dispatch [:database-schema->handle-database-schema connection]))
(defmethod get-database-schema "mssql" [_ connection]
  (rf/dispatch [:database-schema->handle-database-schema connection]))
(defmethod get-database-schema "postgres" [_ connection]
  (rf/dispatch [:database-schema->handle-multi-database-schema connection]))
(defmethod get-database-schema "mysql" [_ connection]
  (rf/dispatch [:database-schema->handle-multi-database-schema connection]))
(defmethod get-database-schema "mongodb" [_ connection]
  (rf/dispatch [:database-schema->handle-multi-database-schema connection]))

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
   [:figure {:class "w-3 flex-shrink-0 animate-spin opacity-60"}
    [:img {:src (str config/webapp-url "/icons/icon-loader-circle-white.svg")}]]])

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
          [:> Text {:size "1"
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
                 [:> Text {:size "1"} field]
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
                [:> Table {:size 12}]
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
                 [:> Text {:size "1"} table]
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
    (fn [schema-name tables connection-name current-schema database-schema-status & {:keys [is-first] :or {is-first false}}]
      (when (and (not @initialized))
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
           [:> Text {:size "1"} schema-name]
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

(defn- database-item [db schema connection-name database-schema-status current-schema]
  (let [is-selected (= db (get-in current-schema [:open-database]))
        loading-databases (get-in current-schema [:loading-databases] #{})
        is-loading-this-db (contains? loading-databases db)
        db-schemas (or (not-empty schema) {})]
    [:div
     [:div {:class "flex items-center gap-smal mb-2"}
      [:span {:class (str "hover:text-blue-500 hover:underline cursor-pointer "
                          (when is-loading-this-db "opacity-75 ")
                          "flex items-center")
              :on-click #(if is-selected
                           (rf/dispatch [:database-schema->close-database {:connection-name connection-name}])
                           (rf/dispatch [:database-schema->change-database
                                         {:connection-name connection-name}
                                         db]))}
       [:> Text {:size "1" :weight "bold"} db]
       (if is-selected
         [:> ChevronDown {:size 12}]
         [:> ChevronRight {:size 12}])]]

     (when is-selected
       [:div
        (cond
          is-loading-this-db
          [loading-indicator "Loading tables..."]

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
          [:> Text {:as "p" :size "1" :mb "2" :ml "2"}
           (if (and (= :error database-schema-status) (:error current-schema))
             (:error current-schema)
             "No tables found")])])]))

(defn- databases-tree []
  (fn [databases schema connection-name database-schema-status current-schema]
    [:div.text-xs
     (doall
      (for [db databases]
        ^{:key db}
        [database-item
         db
         schema
         connection-name
         database-schema-status
         current-schema]))]))

(defn- sql-databases-tree []
  (fn [schema connection-name current-schema database-schema-status]
    [:div
     (cond
       (and (= :error database-schema-status) (:error current-schema))
       [:> Text {:as "p" :size "1" :mb "2" :ml "2"}
        (:error current-schema)]

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
  (case type
    "oracledb" [sql-databases-tree (into (sorted-map) schema) connection-name current-schema database-schema-status]
    "mssql" [sql-databases-tree (into (sorted-map) schema) connection-name current-schema database-schema-status]

    "mysql" [databases-tree databases (into (sorted-map) schema) connection-name database-schema-status current-schema]
    "postgres" [databases-tree databases (into (sorted-map) schema) connection-name database-schema-status current-schema]
    "mongodb" [databases-tree databases (into (sorted-map) schema) connection-name database-schema-status current-schema]

    [:> Text {:size "1"}
     "Couldn't load the schema"]))

(defn tree-view-status [{:keys [status
                                databases
                                schema
                                connection
                                current-schema
                                database-schema-status]}]
  [:div {:class "text-gray-200"}
   (cond
     (and (= status :loading) (empty? schema) (empty? databases))
     [loading-indicator "Loading schema"]

     (= status :failure)
     [:div
      {:class "flex gap-small items-center py-regular text-xs"}
      [:span
       "Couldn't load the schema"]]

     :else
     [db-view {:type (:connection-type connection)
               :schema schema
               :databases databases
               :connection-name (:connection-name connection)
               :database-schema-status database-schema-status
               :current-schema current-schema}])])

(defn main [connection]
  (let [database-schema (rf/subscribe [::subs/database-schema])
        local-connection (r/atom (:connection-name connection))
        ;; Store the schema state locally to avoid re-renders
        ;; when there are no actual changes
        local-schema-state (r/atom nil)
        ;; Flag para controlar se já iniciamos o carregamento
        loading-started (r/atom false)]

    (when (and connection
               (:connection-name connection)
               (not @loading-started))
      (reset! loading-started true)
      (get-database-schema (:connection-type connection) connection))

    ;; Using memoization for the main component
    (r/create-class
     {:component-did-mount
      (fn []
        (when-let [schema (get-in @database-schema [:data @local-connection])]
          (reset! local-schema-state schema))
        (when (and (#{:postgres :mongodb} (keyword (:connection-type connection)))
                   (.getItem js/localStorage "selected-database"))
          (rf/dispatch [:database-schema->change-database
                        {:connection-name (:connection-name connection)}
                        (.getItem js/localStorage "selected-database")])))

      :component-did-update
      (fn [this old-argv]
        (let [[_ old-conn] old-argv
              [_ new-conn] (r/argv this)]
          (when (not= (:connection-name old-conn) (:connection-name new-conn))
            (reset! local-connection (:connection-name new-conn))
            (reset! loading-started false) ;; Resetar o flag para permitir novo carregamento
            (get-database-schema (:connection-type new-conn) new-conn))))

      :should-component-update
      (fn [this old-argv]
        (let [[_ old-conn] old-argv
              [_ new-conn] (r/argv this)
              old-schema (get-in @database-schema [:data (:connection-name old-conn)])
              new-schema (get-in @database-schema [:data (:connection-name new-conn)])]
          ;; Only updates when the connection or the schema actually change
          (or (not= (:connection-name old-conn) (:connection-name new-conn))
              (not= (:status old-schema) (:status new-schema))
              (not= (:database-schema-status old-schema) (:database-schema-status new-schema))
              (not= (:loading-columns old-schema) (:loading-columns new-schema))
              (not= (:columns-cache old-schema) (:columns-cache new-schema))
              (and (= (:status new-schema) :success)
                   (not= @local-schema-state new-schema)))))

      :reagent-render
      (fn [{:keys [connection-type connection-name]}]
        (let [current-schema (get-in @database-schema [:data connection-name])]
          (when (and (= (:status current-schema) :success)
                     (not= @local-schema-state current-schema))
            (reset! local-schema-state current-schema))

          (tree-view-status
           {:status (:status current-schema)
            :databases (:databases current-schema)
            :schema (:schema-tree current-schema)
            :connection connection
            :database-schema-status (:database-schema-status current-schema)
            :current-schema current-schema})))})))
