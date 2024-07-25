(ns webapp.events.connections
  (:require [re-frame.core :as rf]
            [webapp.connections.constants :as constants]))

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
 ::connections->set-updating-connection
 (fn
   [{:keys [db]} [_ connection]]
   {:db (assoc db :connections->updating-connection {:loading false :data connection})}))

(rf/reg-event-fx
 :connections->get-connection
 (fn
   [{:keys [db]} [_ data]]
   {:db (assoc db :connections->updating-connection {:loading true :data {}})
    :fx [[:dispatch
          [:fetch {:method "GET"
                   :uri (str "/connections/" (:connection-name data))
                   :on-success (fn [connection]
                                 (rf/dispatch [::connections->set-updating-connection connection]))}]]]}))

(rf/reg-event-fx
 :connections->get-connections
 (fn
   [{:keys [db]} [_ _]]
   {:db (assoc-in db [:connections :loading] true)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/connections"
                             :on-success #(rf/dispatch [::connections->set-connections %])}]]]}))

(rf/reg-event-fx
 ::connections->set-connections
 (fn
   [{:keys [db]} [_ connections]]
   {:db (assoc db :connections {:results connections :loading false})}))

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
                                      (rf/dispatch [:close-modal])
                                      (rf/dispatch [:show-snackbar
                                                    {:level :success
                                                     :text (str "Connection " (:name connection) " updated!")}])
                                      (rf/dispatch [:connections->get-connections])
                                      (rf/dispatch [:navigate :connections]))}]]]})))

(rf/reg-event-fx
 :connections->connection-connect
 (fn
   [{:keys [db]} [_ connection-name]]
   (let [body {:connection_name connection-name :port "8999" :access_duration 1800000000000}]
     {:db (assoc-in db [:connections->connection-connected] {:data body :status :loading})
      :fx [[:dispatch [:fetch
                       {:method "POST"
                        :uri "/proxymanager/connect"
                        :body body
                        :on-failure (fn [err]
                                      (rf/dispatch [::connections->connection-connected-error (merge body {:error-message err})]))
                        :on-success (fn [res]
                                      (rf/dispatch [:show-snackbar {:level :success
                                                                    :text (str "The connection " connection-name " is connected!")}])
                                      (rf/dispatch [::connections->connection-connected-success res]))}]]]})))

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
                                    (rf/dispatch [::connections->connection-connected-success res]))
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

(rf/reg-event-fx
 :connections->quickstart-create-postgres-demo
 (fn [{:keys [db]} [_]]
   {:fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/agents"
                      :on-success (fn [agents]
                                    (let [agent (first agents)
                                          connection (merge constants/connection-postgres-demo
                                                            {:agent_id (:id agent)})]
                                      (rf/dispatch [::connections->quickstart-create-connection connection])))}]]]}))

(rf/reg-event-fx
 :connections->delete-connection
 (fn
   [{:keys [db]} [_ connection-name]]
   {:fx [[:dispatch
          [:fetch {:method "DELETE"
                   :uri (str "/connections/" connection-name)
                   :on-success (fn [] (rf/dispatch [:navigate :connections]))}]]]}))
