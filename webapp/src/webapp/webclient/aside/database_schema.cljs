(ns webapp.webclient.aside.database-schema
  (:require ["@radix-ui/themes" :refer [Em Text]]
            ["lucide-react" :refer [ChevronDown ChevronRight Hash Database File FolderClosed FolderOpen Table]]
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
  (rf/dispatch [:database-schema->handle-database-schema connection]))
(defmethod get-database-schema "mongodb" [_ connection]
  (rf/dispatch [:database-schema->handle-multi-database-schema connection]))

(defn- field-type-tree [type]
  [:div {:class "text-xs pl-regular italic"}
   (str "(" type ")")])

(defn- indexes-columns-tree []
  (fn [columns]
    [:div {:class "pl-small"}
     (doall
      (for [[number column] (into (sorted-map) columns)]
        ^{:key (str number column)}
        [:div {:class "flex items-center gap-small mb-2"}
         [:> Hash {:size 14}]
         [:> Text {:size "1" :class "px-1"}
          (str number " -")]
         (doall
          (for [[column-name _] column]
            ^{:key column-name}
            [:> Text {:size "1"} column-name]))]))]))

(defn- indexes-tree [_]
  (let [dropdown-status (r/atom :closed)]
    (fn [indexes]
      [:div {:class "pl-small"}
       [:div
        [:div {:class "flex items-center gap-small mb-2"}
         (if (= @dropdown-status :closed)
           [:> FolderClosed {:size 12}]
           [:> FolderOpen {:size 12}])
         [:> Text {:size "1"
                   :class (str "hover:underline cursor-pointer "
                               "flex items-center")
                   :on-click #(reset! dropdown-status
                                      (if (= @dropdown-status :open) :closed :open))}
          "Indexes"
          (if (= @dropdown-status :open)
            [:> ChevronDown {:size 12}]
            [:> ChevronRight {:size 12}])]]]
       [:div {:class (when (not= @dropdown-status :open)
                       "h-0 overflow-hidden")}
        (doall
         (for [[index columns-names] indexes]
           ^{:key index}
           [:div {:class "pl-small"}
            [:div {:class "flex items-center gap-small mb-2"}
             [:> Hash {:size 14}]
             [:> Text {:size "1" :class "flex items-center"}
              index]]
            [indexes-columns-tree columns-names]]))]])))

(defn- fields-tree [fields]
  (let [dropdown-status (r/atom {})
        dropdown-columns-status (r/atom :closed)]
    (fn []
      [:div {:class "pl-small"}
       [:div
        [:div {:class "flex items-center gap-small mb-2"}
         (if (= @dropdown-columns-status :closed)
           [:> FolderClosed {:size 12}]
           [:> FolderOpen {:size 12}])
         [:> Text {:size "1"
                   :class (str "hover:underline cursor-pointer "
                               "flex items-center")
                   :on-click #(reset! dropdown-columns-status
                                      (if (= @dropdown-columns-status :open) :closed :open))}
          "Columns"
          (if (= @dropdown-columns-status :open)
            [:> ChevronDown {:size 12}]
            [:> ChevronRight {:size 12}])]]]
       [:div {:class (str "pl-small" (when (not= @dropdown-columns-status :open)
                                       " h-0 overflow-hidden"))}
        (doall
         (for [[field field-type] fields]
           ^{:key field}
           [:div
            [:div {:class "flex items-center gap-small mb-2"}
             [:> File {:size 12}]
             [:span {:class (str "hover:text-blue-500 hover:underline cursor-pointer "
                                 "flex items-center")
                     :on-click #(swap! dropdown-status
                                       assoc-in [field]
                                       (if (= (get @dropdown-status field) :open) :closed :open))}
              [:> Text {:size "1"} field]
              (if (= (get @dropdown-status field) :open)
                [:> ChevronDown {:size 12}]
                [:> ChevronRight {:size 12}])]]
            [:div {:class (when (not= (get @dropdown-status field) :open)
                            "h-0 overflow-hidden")}
             [field-type-tree (first (map key field-type))]]]))]])))

(defn- tables-tree []
  (let [dropdown-status (r/atom {})]
    (fn [tables indexes]
      [:div {:class "pl-small"}
       (doall
        (for [[table fields] tables]
          ^{:key table}
          [:div
           [:div {:class "flex items-center gap-small mb-2"}
            [:> Table {:size 12}]
            [:span {:class (str "hover:text-blue-500 hover:underline cursor-pointer "
                                "flex items-center")
                    :on-click #(swap! dropdown-status
                                      assoc-in [table]
                                      (if (= (get @dropdown-status table) :open) :closed :open))}
             [:> Text {:size "1"} table]
             (if (= (get @dropdown-status table) :open)
               [:> ChevronDown {:size 12}]
               [:> ChevronRight {:size 12}])]]
           [:div {:class (when (not= (get @dropdown-status table) :open)
                           "h-0 overflow-hidden")}
            [fields-tree (into (sorted-map) fields)]
            [indexes-tree (into (sorted-map) (get indexes table))]]]))])))

(defn- sql-databases-tree [_]
  (let [dropdown-status (r/atom {})]
    (fn [schema indexes has-database?]
      [:div {:class (when has-database?
                      "pl-small")}
       (doall
        (for [[db tables] schema]
          ^{:key db}
          [:div
           [:div {:class "flex items-center gap-small mb-2"}
            [:> Database {:size 12}]
            [:span {:class (str "hover:text-blue-500 hover:underline cursor-pointer "
                                "flex items-center")
                    :on-click #(swap! dropdown-status
                                      assoc-in [db]
                                      (if (= (get @dropdown-status db) :closed) :open :closed))}
             [:> Text {:size "1"} db]
             (if (not= (get @dropdown-status db) :closed)
               [:> ChevronDown {:size 12}]
               [:> ChevronRight {:size 12}])]]
           [:div {:class (when (= (get @dropdown-status db) :closed)
                           "h-0 overflow-hidden")}
            [tables-tree (into (sorted-map) tables) (into (sorted-map) (get indexes db))]]]))])))

(defn- databases-tree []
  (let [open-database (r/atom nil)]
    (fn [databases schema indexes connection-name database-schema-status current-schema]
      [:div.text-xs
       (doall
        (for [db databases]
          ^{:key db}
          [:div
           [:div {:class "flex items-center gap-smal mb-2"}
            [:span {:class (str "hover:text-blue-500 hover:underline cursor-pointer "
                                "flex items-center ")
                    :on-click (fn []
                                (reset! open-database (when (not= @open-database db) db))
                                (if @open-database
                                  (rf/dispatch [:database-schema->change-database
                                                {:connection-name connection-name}
                                                db])

                                  (rf/dispatch [:database-schema->clear-selected-database connection-name])))}
             [:> Text {:size "1" :weight "bold"} db]
             (if (= @open-database db)
               [:> ChevronDown {:size 12}]
               [:> ChevronRight {:size 12}])]]
           [:div {:class (when (not= @open-database db)
                           "h-0 overflow-hidden")}

            (cond
              (= :loading database-schema-status)
              [:div
               {:class "flex gap-small items-center pb-small ml-small text-xs"}
               [:span {:class "italic"}
                "Loading tables and indexes"]
               [:figure {:class "w-3 flex-shrink-0 animate-spin opacity-60"}
                [:img {:src (str config/webapp-url "/icons/icon-loader-circle-white.svg")}]]]

              (and (= :error database-schema-status) (:error current-schema))
              [:> Text {:as "p" :size "1" :mb "2" :ml "2"}
               (:error current-schema)]

              (empty? schema)
              [:> Text {:as "p" :size "1" :mb "2" :ml "2"}
               "Couldn't load tables for this database"]

              :else
              [sql-databases-tree schema indexes true])]]))])))

(defn db-view [{:keys [type
                       schema
                       indexes
                       databases
                       connection-name
                       current-schema
                       database-schema-status]}]
  (case type
    "oracledb" [sql-databases-tree (into (sorted-map) schema) (into (sorted-map) indexes) false]
    "mssql" [sql-databases-tree (into (sorted-map) schema) (into (sorted-map) indexes) false]
    "postgres" [databases-tree databases (into (sorted-map) schema) (into (sorted-map) indexes) connection-name database-schema-status current-schema]
    "mysql" [sql-databases-tree (into (sorted-map) schema) (into (sorted-map) indexes) false]
    "mongodb" [databases-tree databases (into (sorted-map) schema) (into (sorted-map) indexes) connection-name database-schema-status current-schema]
    [:> Text {:size "1"}
     "Couldn't load the schema"]))

(defn tree-view-status [{:keys [status
                                databases
                                schema
                                indexes
                                connection
                                current-schema
                                database-schema-status]}]
  (cond
    (and (= :error database-schema-status) (:error current-schema))
    [:> Text {:as "p" :size "1" :my "4"}
     (:error current-schema)]

    (= status :loading)
    [:div
     {:class "flex gap-small items-center py-regular text-xs"}
     [:span {:class "italic"}
      "Loading schema"]
     [:figure {:class "w-3 flex-shrink-0 animate-spin opacity-60"}
      [:img {:src (str config/webapp-url "/icons/icon-loader-circle-white.svg")}]]]

    (= status :failure)
    [:div
     {:class "flex gap-small items-center py-regular text-xs"}
     [:span
      "Couldn't load the schema"]]

    (= status :success)
    [db-view {:type (:connection-type connection)
              :schema schema
              :indexes indexes
              :databases databases
              :connection-name (:connection-name connection)
              :database-schema-status database-schema-status
              :current-schema current-schema}]

    :else
    [:div
     {:class "flex gap-small items-center py-regular text-xs"}
     [:span {:class "italic"}
      "Loading schema"]
     [:figure {:class "w-3 flex-shrink-0 animate-spin opacity-60"}
      [:img {:src (str config/webapp-url "/icons/icon-loader-circle-white.svg")}]]]))

(defn main [connection]
  (let [database-schema (rf/subscribe [::subs/database-schema])
        local-connection (r/atom (:connection-name connection))]

    (when (and connection (:connection-name connection))
      (get-database-schema (:connection-type connection) connection))

    (fn [{:keys [connection-type connection-name]}]
      (when (not= @local-connection connection-name)
        (reset! local-connection connection-name)
        (get-database-schema connection-type {:connection-type connection-type
                                              :connection-name connection-name}))

      (let [current-schema (get-in @database-schema [:data connection-name])]

        [:div {:class "text-gray-200"}
         [tree-view-status
          {:status (:status current-schema)
           :databases (:databases current-schema)
           :schema (:schema-tree current-schema)
           :indexes (:indexes-tree current-schema)
           :connection connection
           :database-schema-status (:database-schema-status current-schema)
           :current-schema current-schema}]]))))
