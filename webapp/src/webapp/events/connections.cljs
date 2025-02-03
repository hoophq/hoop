(ns webapp.events.connections
  (:require [re-frame.core :as rf]
            [clojure.edn :refer [read-string]]
            [webapp.connections.constants :as constants]
            [webapp.connections.views.connection-connect :as connection-connect]))

(rf/reg-event-fx
 :connections->get-connection-details
 (fn
   [{:keys [db]} [_ connection-name]]
   {:db (assoc db :connections->connection-details {:loading true :data {:name connection-name}})
    :fx [[:dispatch
          [:fetch {:method "GET"
                   :uri (str "/connections/" connection-name)
                   :on-success (fn [connection]
                                 (rf/dispatch [:connections->set-connection connection]))}]]]}))

(rf/reg-event-fx
 :connections->set-connection
 (fn
   [{:keys [db]} [_ connection]]
   {:db (assoc db :connections->connection-details {:loading false :data connection})}))

(rf/reg-event-fx
 :connections->get-connections
 (fn
   [{:keys [db]} [_ _]]
   {:db (assoc-in db [:connections :loading] true)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/connections"
                             :on-success #(rf/dispatch [:connections->set-connections %])}]]]}))

(rf/reg-event-fx
 :connections->set-connections
 (fn
   [{:keys [db]} [_ connections]]
   {:db (assoc db :connections {:results connections :loading false})}))

(rf/reg-event-fx
 :connections->filter-connections
 (fn [_ [_ query-params]]
   {:fx [[:dispatch [:navigate :connections query-params]]]}))

(rf/reg-event-fx
 :connections->create-connection
 (fn
   [{:keys [db]} [_ connection]]
   (let [body (apply merge (for [[k v] connection :when (not (= "" v))] {k v}))]
     {:fx [[:dispatch [:fetch
                       {:method "POST"
                        :uri "/connections"
                        :body body
                        :on-success (fn [connection]
                                      (rf/dispatch [:close-modal])
                                     ;; plugins might be updated in the connection
                                     ;; creation action, so we get them again here
                                      (rf/dispatch [:plugins->get-my-plugins])
                                      (rf/dispatch [:connections->get-connections])
                                      (rf/dispatch [:show-snackbar {:level :success
                                                                    :text "Connection created!"}])

                                      (rf/dispatch [:navigate :connections]))}]]]})))


(rf/reg-event-fx
 :connections->update-connection
 (fn
   [{:keys [db]} [_ connection]]
   (let [body (apply merge (for [[k v] connection :when (not (= "" v))] {k v}))]
     {:fx [[:dispatch [:fetch
                       {:method "PUT"
                        :uri (str "/connections/" (:name connection))
                        :body body
                        :on-success (fn []
                                      (rf/dispatch [:modal->close])
                                      (rf/dispatch [:show-snackbar
                                                    {:level :success
                                                     :text (str "Connection " (:name connection) " updated!")}])
                                      (rf/dispatch [:plugins->get-my-plugins])
                                      (rf/dispatch [:connections->get-connections])
                                      (rf/dispatch [:navigate :connections]))}]]]})))

(rf/reg-event-fx
 :connections->connection-connect
 (fn
   [{:keys [db]} [_ connection]]
   (let [body {:connection_name (:name connection)
               :port "8999"
               :access_duration 1800000000000}]
     {:db (assoc-in db [:connections->connection-connected] {:data body :status :loading})
      :fx [[:dispatch [:fetch
                       {:method "POST"
                        :uri "/proxymanager/connect"
                        :body body
                        :on-failure (fn [err]
                                      (rf/dispatch [::connections->connection-connected-error (merge body {:error-message err})]))
                        :on-success (fn [res]
                                      (if (= (:status res) "disconnected")
                                        (do
                                          (rf/dispatch [:show-snackbar {:level :error
                                                                        :text (str "The connection " (:name connection) " is not able "
                                                                                   "to be connected, please contact your admin.")}])
                                          (rf/dispatch [:modal->close]))
                                        (do
                                          (rf/dispatch [:show-snackbar {:level :success
                                                                        :text (str "The connection " (:name connection) " is connected!")}])
                                          (rf/dispatch [::connections->connection-connected-success res]))))}]]]})))

(rf/reg-event-fx
 :connections->connection-disconnect
 (fn
   [{:keys [db]} [_]]
   (let [connection-name (-> db :connections->connection-connected :data :connection_name)]
     {:fx [[:dispatch [:fetch
                       {:method "POST"
                        :uri "/proxymanager/disconnect"
                        :on-success (fn [res]
                                      (rf/dispatch [:show-snackbar {:level :success
                                                                    :text (str "The connection " connection-name " was disconnected!")}])
                                      (rf/dispatch [::connections->connection-connected-success res]))
                        :on-failure #(println :failure :connections->connection-disconnect %)}]]]})))

(rf/reg-event-fx
 :connections->connection-get-status
 (fn
   [{:keys [db]} [_]]
   {:fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/proxymanager/status"
                      :on-success (fn [res]
                                    (rf/dispatch [::connections->connection-connected-success res])
                                    (when (= (:status res) "connected")
                                      (rf/dispatch [:modal->open {:content  [connection-connect/main]
                                                                  :maxWidth "446px"
                                                                  :custom-on-click-out connection-connect/minimize-modal}])))
                      :on-failure #(println :failure :connections->connection-get-status %)}]]]}))

(rf/reg-event-fx
 ::connections->connection-connected-success
 (fn
   [{:keys [db]} [_ connection]]
   {:db (assoc db :connections->connection-connected {:data connection :status :ready})}))

(rf/reg-event-fx
 ::connections->connection-connected-error
 (fn
   [{:keys [db]} [_ err]]
   {:db (assoc db :connections->connection-connected {:data err :status :failure})}))

(rf/reg-event-fx
 ::connections->quickstart-create-connection
 (fn [{:keys [db]} [_ connection]]
   (let [body (apply merge (for [[k v] connection :when (not (= "" v))] {k v}))]
     {:fx [[:dispatch
            [:fetch
             {:method "POST"
              :uri "/connections"
              :body body
              :on-success (fn []
                            (rf/dispatch [:show-snackbar {:level :success
                                                          :text "Connection created!"}])
                            (rf/dispatch [:connections->get-connections])
                            (rf/dispatch [:plugins->get-my-plugins])
                            (rf/dispatch [:navigate :home]))}]]]})))

(def quickstart-query
  "SELECT c.firstname, c.lastname, o.orderid, o.orderdate, SUM(ol.quantity) AS total_quantity, SUM(ol.quantity * p.price) AS total_amount
FROM customers c
JOIN orders o ON c.customerid = o.customerid
JOIN orderlines ol ON o.orderid = ol.orderid
JOIN products p ON ol.prod_id = p.prod_id
WHERE c.country = 'US'
GROUP BY c.firstname, c.lastname, o.orderid, o.orderdate
ORDER BY total_amount DESC;")

(rf/reg-event-fx
 :connections->quickstart-create-postgres-demo
 (fn [{:keys [db]} [_]]
   {:fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/agents"
                      :on-success (fn [agents]
                                    (let [agent (first agents)
                                          connection (merge constants/connection-postgres-demo
                                                            {:agent_id (:id agent)})
                                          code-tmp-db {:date (.now js/Date)
                                                       :code quickstart-query}
                                          code-tmp-db-json (.stringify js/JSON (clj->js code-tmp-db))]

                                      (.setItem js/localStorage :code-tmp-db code-tmp-db-json)
                                      (rf/dispatch [::connections->quickstart-create-connection connection])))}]]]}))

(rf/reg-event-fx
 :connections->delete-connection
 (fn
   [{:keys [db]} [_ connection-name]]
   {:fx [[:dispatch
          [:fetch {:method "DELETE"
                   :uri (str "/connections/" connection-name)
                   :on-success (fn []
                                 (let [localstorage-connection
                                       (first
                                        (read-string (.getItem js/localStorage "run-connection-list-selected")))]

                                   (when (= connection-name (:name localstorage-connection))
                                     (.removeItem js/localStorage "run-connection-list-selected"))

                                   (rf/dispatch [:show-snackbar {:level :success
                                                                 :text "Connection deleted!"}])
                                   (rf/dispatch [:connections->get-connections])
                                   (rf/dispatch [:navigate :connections])))}]]]}))

(rf/reg-event-fx
 :connections->start-connect
 (fn [{:keys [db]} [_ connection]]
   (let [gateway-info (-> db :gateway->info)]
     {:db (assoc-in db [:connections->connection-connected] {:data {} :status :loading})
      :fx [[:dispatch [:hoop-app->update-my-configs {:apiUrl (-> gateway-info :data :api_url)
                                                     :grpcUrl (-> gateway-info :data :grpc_url)
                                                     :token (.getItem js/localStorage "jwt-token")}]]
           [:dispatch [:modal->close]]
           [:dispatch [:hoop-app->restart]]
           [:dispatch-later {:ms 2000 :dispatch [:connections->connection-connect connection]}]
           [:dispatch [:modal->open {:content [connection-connect/main]
                                     :maxWidth "446px"
                                     :custom-on-click-out connection-connect/minimize-modal}]]]})))
