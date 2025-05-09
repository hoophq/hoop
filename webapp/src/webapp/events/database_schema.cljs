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
 :database-schema->clear-selected-database
 (fn [{:keys [db]} [_]]
   (.removeItem js/localStorage "selected-database")
   ;; Também limpar o estado do database aberto no app state
   (let [current-connection (get-in db [:database-schema :current-connection])
         current-database (get-in db [:database-schema :data current-connection :open-database])]
     {:db (-> db
              (assoc-in [:database-schema :data current-connection :open-database] nil)
              ;; Também remover o database da lista de loading se estiver lá
              (update-in [:database-schema :data current-connection :loading-databases]
                         (fn [databases] (disj (or databases #{}) current-database))))})))

(rf/reg-event-fx
 :database-schema->clear-schema
 (fn [{:keys [db]} [_]]
   (.removeItem js/localStorage "selected-database")
   {:db (-> db
            (assoc-in [:database-schema :data] nil))}))

;; Eventos para conexões com múltiplos databases (PostgreSQL, MongoDB)
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
                                                         (:databases response)]))}]]]}))

(rf/reg-event-db
 :database-schema->set-multi-databases
 (fn [db [_ connection databases]]
   (-> db
       (assoc-in [:database-schema :data (:connection-name connection) :databases] databases)
       (assoc-in [:database-schema :data (:connection-name connection) :status] :success))))

;; Eventos comuns para todos os tipos de conexão
(rf/reg-event-fx
 :database-schema->set-schema-error-size-exceeded
 (fn [{:keys [db]} [_ connection error]]
   {:db (-> db
            ;; Mark the general status as error
            (assoc-in [:database-schema :data (:connection-name connection) :status] :success)
            ;; Mark the schema status as error
            (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :error)
            ;; Define the error message
            (assoc-in [:database-schema :data (:connection-name connection) :error]
                      (or error "Schema size too large to display.")))}))

;; Eventos para carregamento de tabelas (para bancos de database único)
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

;; Eventos para carregamento de tabelas (para bancos com múltiplos databases)
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
                     (get-in db [:database-schema :data (:connection-name connection) :current-database]))]
     (-> db
         (assoc-in [:database-schema :data (:connection-name connection) :status] :success)
         (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :success)
         (assoc-in [:database-schema :data (:connection-name connection) :type] (:subtype connection))
         (assoc-in [:database-schema :data (:connection-name connection) :current-database] open-db)
         (assoc-in [:database-schema :data (:connection-name connection) :open-database] open-db)
         (assoc-in [:database-schema :data (:connection-name connection) :schema-tree] (process-tables response))
         (assoc-in [:database-schema :data (:connection-name connection) :columns-cache] {})
         (assoc-in [:database-schema :data (:connection-name connection) :loading-columns] #{})

         (update-in [:database-schema :data (:connection-name connection) :loading-databases]
                    (fn [databases] (disj (or databases #{}) database)))))))

;; Eventos para carregamento progressivo de colunas
(rf/reg-event-fx
 :database-schema->load-columns
 (fn [{:keys [db]} [_ connection-name database table-name schema-name]]
   (let [cache-key (str schema-name "." table-name)
         uri (if database
               ;; Se tiver database, incluir na URI
               (str "/connections/" connection-name
                    "/columns?database=" database
                    "&table=" table-name
                    "&schema=" schema-name)
               ;; Caso contrário, não incluir database na URI
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

(rf/reg-event-db
 :database-schema->columns-failure
 (fn [db [_ connection-name schema-name table-name error]]
   (let [cache-key (str schema-name "." table-name)]
     (-> db
         (update-in [:database-schema :data connection-name :loading-columns] disj cache-key)
         (assoc-in [:database-schema :data connection-name :columns-cache cache-key]
                   {:error (or (.-message error) "Failed to load columns")})))))

;; Evento para mudança de database (apenas para PostgreSQL e MongoDB)
(rf/reg-event-fx
 :database-schema->change-database
 (fn [{:keys [db]} [_ connection database]]
   (let [current-db (get-in db [:database-schema :data (:connection-name connection) :current-database])
         loading-databases (get-in db [:database-schema :data (:connection-name connection) :loading-databases] #{})
         already-loading? (contains? loading-databases database)]

     ;; Guardar o database selecionado no localStorage
     (.setItem js/localStorage "selected-database" database)

     ;; Se já estiver carregando este database ou já for o database atual, não fazer nada
     (if (or already-loading? (= database current-db))
       {}
       ;; Caso contrário, iniciar o carregamento
       {:db (-> db
                (assoc-in [:database-schema :data (:connection-name connection) :open-database] database)
                (assoc-in [:database-schema :data (:connection-name connection) :current-database] database)
                (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :loading)
                (update-in [:database-schema :data (:connection-name connection) :loading-databases]
                           (fn [databases] (conj (or databases #{}) database))))
        :fx [[:dispatch [:database-schema->load-tables connection database]]]}))))
