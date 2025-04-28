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
  (rf/dispatch [:database-schema->handle-database-schema connection]))
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

;; Cache para evitar renderizações desnecessárias
(def render-cache (atom {}))

;; Limites para evitar renderização excessiva
(def default-max-items 50)
(def max-expanded-tables 10)

(defn- fields-tree [fields]
  (let [dropdown-status (r/atom {})
        dropdown-columns-status (r/atom :closed)
        is-typing (r/atom false)]

    (fn []
      (let [current-status @dropdown-status
            current-columns-status @dropdown-columns-status
            typing? (boolean (aget js/window "is_typing"))
            limit (if typing? 15 default-max-items)]
        [:div {:class "pl-small"}
         [:div
          [:div {:class "flex items-center gap-small mb-2"}
           (if (= current-columns-status :closed)
             [:> FolderClosed {:size 12}]
             [:> FolderOpen {:size 12}])
           [:> Text {:size "1"
                     :class (str "hover:underline cursor-pointer "
                                 "flex items-center")
                     :on-click #(reset! dropdown-columns-status
                                        (if (= current-columns-status :open) :closed :open))}
            "Columns"
            (if (= current-columns-status :open)
              [:> ChevronDown {:size 12}]
              [:> ChevronRight {:size 12}])]]]

         [:div {:class (str "pl-small" (when (not= current-columns-status :open)
                                         " h-0 overflow-hidden"))}
          (when (= current-columns-status :open)
            [:div
             ;; Lista de campos com limite
             (doall
              (for [[field field-type] (take limit (seq fields))]
                ^{:key field}
                [:div
                 [:div {:class "flex items-center gap-small mb-2"}
                  [:> File {:size 12}]
                  [:span {:class (str "hover:text-blue-500 hover:underline cursor-pointer "
                                      "flex items-center")
                          :on-click #(swap! dropdown-status
                                            assoc-in [field]
                                            (if (= (get current-status field) :open) :closed :open))}
                   [:> Text {:size "1"} field]
                   (if (= (get current-status field) :open)
                     [:> ChevronDown {:size 12}]
                     [:> ChevronRight {:size 12}])]]
                 [:div {:class (when (not= (get current-status field) :open)
                                 "h-0 overflow-hidden")}
                  (when (and (= (get current-status field) :open) (not typing?))
                    [field-type-tree (first (map key field-type))])]]))

             ;; Contador de campos restantes
             (when (> (count fields) limit)
               [:div {:class "text-xs text-gray-500 italic mt-2"}
                (str "+" (- (count fields) limit) " more columns"
                     (when typing? " (typing mode)"))])])]]))))

(defn- tables-tree []
  (let [dropdown-status (r/atom {})
        expanded-count (r/atom 0)]

    (fn [tables]
      (let [current-status @dropdown-status
            typing? (boolean (aget js/window "is_typing"))
            limit (if typing? 30 default-max-items)
            cache-key (str "tables-" (hash tables) "-" typing?)]

        ;; Usar cache para evitar re-renderização desnecessária
        (if-let [cached (@render-cache cache-key)]
          cached
          (let [render-result
                [:div {:class "pl-small"}
                 ;; Limitar número de tabelas renderizadas
                 (doall
                  (for [[table fields] (take limit (seq tables))]
                    ^{:key table}
                    [:div
                     [:div {:class "flex items-center gap-small mb-2"}
                      [:> Table {:size 12}]
                      [:span {:class (str "hover:text-blue-500 hover:underline cursor-pointer "
                                          "flex items-center")
                              :on-click (fn []
                                          ;; Limitar número de tabelas expandidas simultaneamente
                                          (when (and (not= (get current-status table) :open)
                                                     (>= @expanded-count max-expanded-tables))
                                            (let [first-open (first (filter #(= (val %) :open) current-status))]
                                              (when first-open
                                                (swap! dropdown-status assoc (key first-open) :closed))))

                                          (swap! dropdown-status
                                                 assoc-in [table]
                                                 (if (= (get current-status table) :open)
                                                   (do (swap! expanded-count dec) :closed)
                                                   (do (swap! expanded-count inc) :open))))}
                       [:> Text {:size "1"} table]
                       (if (= (get current-status table) :open)
                         [:> ChevronDown {:size 12}]
                         [:> ChevronRight {:size 12}])]]
                     [:div {:class (when (not= (get current-status table) :open)
                                     "h-0 overflow-hidden")}
                      (when (= (get current-status table) :open)
                        [fields-tree (into (sorted-map) fields)])]]))

                 ;; Mostrar mensagem se houver mais tabelas do que estamos exibindo
                 (when (> (count tables) limit)
                   [:div {:class "text-xs text-gray-500 italic mt-2"}
                    (str "+" (- (count tables) limit)
                         " more tables" (when typing? " (typing mode)"))])]]
            ;; Armazenar em cache, mas limitar tamanho do cache
            (when (< (count @render-cache) 10)
              (swap! render-cache assoc cache-key render-result))
            render-result))))))

(defn- sql-databases-tree [_]
  (let [dropdown-status (r/atom {})]
    (fn [schema has-database? current-schema database-schema-status]
      [:div {:class (when has-database?
                      "pl-small")}
       (cond
         (and (= :error database-schema-status) (:error current-schema))
         [:> Text {:as "p" :size "1" :mb "2" :ml "2"}
          (:error current-schema)]

         :else
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
              [tables-tree (into (sorted-map) tables)]]])))])))

(defn- databases-tree []
  (let [open-database (r/atom nil)]
    (fn [databases schema connection-name database-schema-status current-schema]
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

                                  (rf/dispatch [:database-schema->clear-selected-database])))}
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
              [sql-databases-tree schema true])]]))])))

(defn db-view [{:keys [type
                       schema
                       databases
                       connection-name
                       current-schema
                       database-schema-status]}]
  (case type
    "oracledb" [sql-databases-tree (into (sorted-map) schema) false current-schema database-schema-status]
    "mssql" [sql-databases-tree (into (sorted-map) schema) false current-schema database-schema-status]
    "postgres" [databases-tree databases (into (sorted-map) schema) connection-name database-schema-status current-schema]
    "mysql" [sql-databases-tree (into (sorted-map) schema) false current-schema database-schema-status]
    "mongodb" [databases-tree databases (into (sorted-map) schema) connection-name database-schema-status current-schema]
    [:> Text {:size "1"}
     "Couldn't load the schema"]))

(defn tree-view-status [{:keys [status
                                databases
                                schema
                                connection
                                current-schema
                                database-schema-status]}]
  (cond
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
        local-connection (r/atom (:connection-name connection))
        ;; Store the schema state locally to avoid re-renders
        local-schema-state (r/atom nil)]

    (when (and connection (:connection-name connection))
      (get-database-schema (:connection-type connection) connection))

    ;; Limpar cache periodicamente para evitar vazamento de memória
    (js/setInterval #(when (> (count @render-cache) 20)
                       (reset! render-cache {})) 60000)

    ;; Using memoization for the main component
    (r/create-class
     {:component-did-mount
      (fn []
        (when-let [schema (get-in @database-schema [:data @local-connection])]
          (reset! local-schema-state schema)))

      :component-did-update
      (fn [this old-argv]
        (let [[_ old-conn] old-argv
              [_ new-conn] (r/argv this)]
          (when (not= (:connection-name old-conn) (:connection-name new-conn))
            (reset! local-connection (:connection-name new-conn))
            (get-database-schema (:connection-type new-conn) new-conn))))

      :should-component-update
      (fn [this old-argv]
        (let [[_ old-conn] old-argv
              [_ new-conn] (r/argv this)
              old-schema (get-in @database-schema [:data (:connection-name old-conn)])
              new-schema (get-in @database-schema [:data (:connection-name new-conn)])
              is-typing (boolean (aget js/window "is_typing"))]
          ;; Não atualizar durante digitação, a menos que a conexão mude
          (if is-typing
            (not= (:connection-name old-conn) (:connection-name new-conn))
            ;; Only updates when the connection or the schema actually change
            (or (not= (:connection-name old-conn) (:connection-name new-conn))
                (not= (:status old-schema) (:status new-schema))
                (not= (:database-schema-status old-schema) (:database-schema-status new-schema))
                (and (= (:status new-schema) :success)
                     (not= @local-schema-state new-schema))))))

      :component-will-unmount
      (fn []
        ;; Limpar cache ao desmontar para evitar vazamento de memória
        (reset! render-cache {}))

      :reagent-render
      (fn [{:keys [connection-type connection-name]}]
        (let [current-schema (get-in @database-schema [:data connection-name])
              is-typing (boolean (aget js/window "is_typing"))]
          (when (and (= (:status current-schema) :success)
                     (not= @local-schema-state current-schema))
            (reset! local-schema-state current-schema))

          [:div {:class "text-gray-200"}
           ;; Mantenha a árvore sempre visível, mas adicione um indicador visual durante a digitação
           (when is-typing
             [:div {:class "text-xs text-gray-500 italic px-4 py-1"}
              "Performance mode active during typing"])

           [tree-view-status
            {:status (:status current-schema)
             :databases (:databases current-schema)
             :schema (:schema-tree current-schema)
             :connection connection
             :database-schema-status (:database-schema-status current-schema)
             :current-schema current-schema}]]))})))
