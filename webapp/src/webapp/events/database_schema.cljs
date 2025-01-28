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

(defn- clear-selected-database [db connection-name]
  (let [stored-db (.getItem js/localStorage "selected-database")
        current-connection (get-in db [:database-schema :data connection-name])]
    (when (and stored-db
               (some #(= stored-db %) (:databases current-connection)))
      (.removeItem js/localStorage "selected-database"))))

(rf/reg-event-fx
 :database-schema->clear-selected-database
 (fn [{:keys [db]} [_ connection-name]]
   (clear-selected-database db connection-name)
   {}))

(rf/reg-event-fx
 :database-schema->clear-schema
 (fn [{:keys [db]} [_ connection-name]]
   (clear-selected-database db connection-name)
   {:db (-> db
            (update-in [:database-schema :data] dissoc connection-name)
            (assoc-in [:database-schema :current-connection] nil))}))

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

(rf/reg-event-fx
 :database-schema->get-multi-database-schema
 (fn [{:keys [db]} [_ connection database databases]]
   (let [schema-url (str "/connections/" (:connection-name connection) "/schemas?database=" database)]
     {:db (-> db
              (assoc-in [:database-schema :data (:connection-name connection) :database-schema-status] :loading)
              (assoc-in [:database-schema :data (:connection-name connection) :databases] databases))
      :fx [[:dispatch [:database-schema->check-schema-size
                       connection
                       schema-url
                       :database-schema->fetch-multi-database-schema]]]})))

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

(rf/reg-event-fx
 :database-schema->handle-database-schema
 (fn [{:keys [db]} [_ connection]]
   (let [schema-url (str "/connections/" (:connection-name connection) "/schemas")]
     {:db (-> db
              (assoc-in [:database-schema :current-connection] (:connection-name connection))
              (assoc-in [:database-schema :data (:connection-name connection) :status] :loading))
      :fx [[:dispatch [:database-schema->check-schema-size
                       connection
                       schema-url
                       :database-schema->fetch-database-schema]]]})))

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
   {:db (assoc-in db [:database-schema :data (:connection-name connection) :database-schema-status] :loading)
    :fx [[:dispatch [:database-schema->get-multi-database-schema
                     connection
                     database
                     (get-in db [:database-schema :data (:connection-name connection) :databases])]]]}))
