(ns webapp.events.database-schema
  (:require [re-frame.core :as rf]))

(defn- check-schema-size [response]
  (let [content-length (js/parseInt (.. response -headers (get "content-length")))
        max-size (* 4 1024 1024)] ; 4MB in bytes
    (<= content-length max-size)))

(defn- process-schema [schema-data]
  (let [schemas (:schemas schema-data)]
    (reduce (fn [acc schema]
              (let [schema-name (:name schema)
                    tables (reduce (fn [table-acc table]
                                     (assoc table-acc (:name table)
                                            (reduce (fn [col-acc column]
                                                      (assoc col-acc (:name column)
                                                             {(:type column)
                                                              {"nullable" (:nullable column)}}))
                                                    {}
                                                    (:columns table))))
                                   {}
                                   (:tables schema))]
                (assoc acc schema-name tables)))
            {}
            schemas)))

;; Novo processamento para tabelas
(defn- process-tables [tables-data]
  (js/console.log "Processando tabelas:" (clj->js tables-data))
  (reduce (fn [acc schema]
            (let [schema-name (:name schema)
                  tables-list (:tables schema)
                  _ (js/console.log "Schema:" schema-name "Tabelas:" (clj->js tables-list))
                  tables (reduce (fn [table-acc table-name]
                                   (assoc table-acc table-name {}))
                                 {}
                                 tables-list)]
              (assoc acc schema-name tables)))
          {}
          (:schemas tables-data)))

;; Novo processamento para colunas
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

;; Modificado para usar o novo endpoint de tabelas
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

(rf/reg-event-fx
 :database-schema->check-schema-size
 (fn [{:keys [db]} [_ connection url success-event]]
   {:db (assoc-in db [:database-schema :data (:connection-name connection) :database-schema-status] :loading)
    :fx [[:dispatch [:fetch {:method "HEAD"
                             :uri url
                             :on-success (fn [response]
                                           (if (check-schema-size response)
                                             (rf/dispatch [success-event connection url])
                                             (rf/dispatch [:database-schema->set-schema-error-size-exceeded connection])))
                             :on-failure (fn [error]
                                           (rf/dispatch [:database-schema->set-schema-error-size-exceeded connection error]))}]]]}))

;; Novo evento para carregar tabelas
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

;; Evento para processar as tabelas carregadas
(rf/reg-event-db
 :database-schema->tables-loaded
 (fn [db [_ connection database response]]
   (let [open-db (or database
                     (get-in db [:database-schema :data (:connection-name connection) :current-database]))]
     (-> db
         (assoc-in [:database-schema :data (:connection-name connection) :status] :success)
         (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :success)
         (assoc-in [:database-schema :data (:connection-name connection) :type] (:connection-type connection))
         (assoc-in [:database-schema :data (:connection-name connection) :current-database] open-db)
         (assoc-in [:database-schema :data (:connection-name connection) :open-database] open-db)
         (assoc-in [:database-schema :data (:connection-name connection) :schema-tree] (process-tables response))
         (assoc-in [:database-schema :data (:connection-name connection) :columns-cache] {})
         (assoc-in [:database-schema :data (:connection-name connection) :loading-columns] #{})
         ;; Remover o database da lista de loading
         (update-in [:database-schema :data (:connection-name connection) :loading-databases]
                    (fn [databases] (disj (or databases #{}) database)))))))

;; Novo evento para carregar colunas de uma tabela específica
(rf/reg-event-fx
 :database-schema->load-columns
 (fn [{:keys [db]} [_ connection-name database table-name schema-name]]
   (let [cache-key (str schema-name "." table-name)]
     ;; Verificar se já temos essas colunas em cache
     (if (get-in db [:database-schema :data connection-name :columns-cache cache-key])
       ;; Se já temos, não fazemos nada
       {}
       ;; Caso contrário, carregamos
       {:db (update-in db [:database-schema :data connection-name :loading-columns] conj cache-key)
        :fx [[:dispatch [:fetch {:method "GET"
                                 :uri (str "/connections/" connection-name
                                           "/columns?database=" database
                                           "&table=" table-name
                                           "&schema=" schema-name)
                                 :on-success (fn [response]
                                               (rf/dispatch [:database-schema->columns-loaded
                                                             connection-name database schema-name table-name response]))
                                 :on-failure (fn [error]
                                               (rf/dispatch [:database-schema->columns-failure
                                                             connection-name schema-name table-name error]))}]]]}))))

;; Evento para processar as colunas carregadas
(rf/reg-event-db
 :database-schema->columns-loaded
 (fn [db [_ connection-name database schema-name table-name response]]
   (let [cache-key (str schema-name "." table-name)
         columns-map (process-columns response)]
     (-> db
         (update-in [:database-schema :data connection-name :loading-columns] disj cache-key)
         (assoc-in [:database-schema :data connection-name :columns-cache cache-key] columns-map)
         ;; Atualizamos também a árvore de schema para incluir as colunas
         (assoc-in [:database-schema :data connection-name :schema-tree schema-name table-name] columns-map)))))

;; Evento para tratar falhas ao carregar colunas
(rf/reg-event-db
 :database-schema->columns-failure
 (fn [db [_ connection-name schema-name table-name error]]
   (let [cache-key (str schema-name "." table-name)]
     (-> db
         (update-in [:database-schema :data connection-name :loading-columns] disj cache-key)
         (assoc-in [:database-schema :data connection-name :columns-cache cache-key]
                   {:error (or (.-message error) "Failed to load columns")})))))

(rf/reg-event-fx
 :database-schema->get-multi-database-schema
 (fn [{:keys [db]} [_ connection database databases]]
   ;; Usar o novo evento de carregar tabelas
   {:fx [[:dispatch [:database-schema->load-tables connection database]]]}))

(rf/reg-event-fx
 :database-schema->fetch-multi-database-schema
 (fn [{:keys [db]} [_ connection url]]
   {:fx [[:dispatch [:fetch {:method "GET"
                             :uri url
                             :on-success #(rf/dispatch [:database-schema->set-multi-database-schema
                                                        {:schema-payload %
                                                         :database (get-in db [:database-schema :data (:connection-name connection) :current-database])
                                                         :databases (get-in db [:database-schema :data (:connection-name connection) :databases])
                                                         :status :success
                                                         :database-schema-status :success
                                                         :connection connection}])
                             :on-failure #(rf/dispatch [:database-schema->set-schema-error-size-exceeded connection %])}]]]}))

(rf/reg-event-fx
 :database-schema->set-multi-database-schema
 (fn [{:keys [db]} [_ {:keys [schema-payload database databases status database-schema-status connection]}]]
   (let [schema {:status status
                 :data (assoc (-> db :database-schema :data)
                              (:connection-name connection)
                              {:status status
                               :database-schema-status database-schema-status
                               :type (:connection-type connection)
                               :raw schema-payload
                               :schema-tree (process-schema schema-payload)
                               :current-database database
                               :databases databases})}]
     {:db (assoc-in db [:database-schema] schema)})))

;; Modificar para usar o novo endpoint de tabelas
(rf/reg-event-fx
 :database-schema->handle-database-schema
 (fn [{:keys [db]} [_ connection]]
   (let [db-name (-> (get-in connection [:secrets "envvar:DB"] "")
                     js/atob)]
     (if (empty? db-name)
       ;; Se não tiver um banco padrão, mostramos erro
       {:db (-> db
                (assoc-in [:database-schema :data (:connection-name connection) :status] :success)
                (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :error)
                (assoc-in [:database-schema :data (:connection-name connection) :error] "No default database configured"))}
       ;; Se tiver, carregamos as tabelas
       {:fx [[:dispatch [:database-schema->load-tables connection db-name]]]}))))

(rf/reg-event-fx
 :database-schema->fetch-database-schema
 (fn [{:keys [db]} [_ connection]]
   {:fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/connections/" (:connection-name connection) "/schemas")
                             :on-success #(rf/dispatch [:database-schema->set-database-schema
                                                        {:schema-payload %
                                                         :status :success
                                                         :database-schema-status :success
                                                         :connection connection}])
                             :on-failure #(rf/dispatch [:database-schema->set-schema-error-size-exceeded connection %])}]]]}))

(rf/reg-event-fx
 :database-schema->set-database-schema
 (fn [{:keys [db]} [_ {:keys [schema-payload status database-schema-status connection]}]]
   (let [schema {:status status
                 :data (assoc (-> db :database-schema :data)
                              (:connection-name connection)
                              {:status status
                               :database-schema-status database-schema-status
                               :type (:connection-type connection)
                               :raw schema-payload
                               :schema-tree (process-schema schema-payload)})}]
     {:db (assoc-in db [:database-schema] schema)})))

;; Event unified to handle schema for all databases
(rf/reg-event-fx
 :database-schema->change-database
 (fn [{:keys [db]} [_ connection database]]
   (.setItem js/localStorage "selected-database" database)
   ;; Marcar imediatamente que o database está em loading
   {:db (-> db
            (assoc-in [:database-schema :data (:connection-name connection) :open-database] database)
            (assoc-in [:database-schema :data (:connection-name connection) :current-database] database)
            (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :loading)
            ;; Adicionar o database à lista de databases em loading
            (update-in [:database-schema :data (:connection-name connection) :loading-databases]
                       (fn [databases] (conj (or databases #{}) database))))
    :fx [[:dispatch [:database-schema->load-tables connection database]]]}))
