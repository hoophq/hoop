(ns webapp.events.plugins
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :plugins->get-my-plugins
 (fn
   [{:keys [db]} [_]]
   (let [success #(rf/dispatch [::plugins->set-my-plugins %])
         get-my-plugins [:fetch
                         {:method "GET"
                          :uri "/plugins"
                          :on-success success
                          :on-failure #(println :failure :get-my-plugins %)}]]
     {:fx [[:dispatch get-my-plugins]]})))

(rf/reg-event-fx
 ::plugins->set-my-plugins
 (fn
   [{:keys [db]} [_ response]]
   {:db (assoc-in db [:plugins->my-plugins] response)}))

(rf/reg-event-fx
 :plugins->get-plugin-by-name
 (fn
   [{:keys [db]} [_ name]]
   {:fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/plugins/" name)
                             :on-success #(rf/dispatch [:plugins->set-plugin (merge %
                                                                                    {:installed? true})])
                             :on-failure #(rf/dispatch [:plugins->set-plugin {:name name
                                                                              :installed? false}])}]]]
    :db (assoc-in db [:plugins->plugin-details :status] :loading)}))

(rf/reg-event-db
 :plugins->set-plugin
 (fn
   [db [_ plugin]]
   (assoc db :plugins->plugin-details
          {:plugin plugin
           :status :ready})))

(rf/reg-event-fx
 :plugins->update-plugin-connections
 (fn
   ;; action -> :add or :remove
   ;; plugin -> the plugin how it is
   ;; connection -> the connection to be added or removed
   [{:keys [db]} [_ {:keys [action plugin connection]}]]
   (let [connections (case action
                       :add (conj
                             (:connections plugin)
                             {:id (:id connection)})
                       :remove (remove #(= (:id connection) (:id %))
                                       (:connections plugin)))
         payload (merge plugin {:connections connections})
         on-success (fn []
                      (rf/dispatch [:show-snackbar {:level :success
                                                    :text "Plugin updated"}])
                      (rf/dispatch [:plugins->get-plugin-by-name (:name plugin)]))
         on-failure (fn [error]
                      (rf/dispatch [:show-snackbar {:level :error
                                                    :text "Failed to update plugin connections"
                                                    :details error}]))]
     {:fx [[:dispatch [:fetch {:method "PUT"
                               :uri (str "/plugins/" (:name plugin))
                               :body payload
                               :on-success on-success
                               :on-failure on-failure}]]]})))

(rf/reg-event-fx
 :plugins->create-plugin
 (fn
   [_ [_ body]]
   (let [success (fn []
                   (rf/dispatch [:show-snackbar {:text "Plugin created!"
                                                 :level :success}])
                   (rf/dispatch [:plugins->get-my-plugins]))
         failure (fn [error]
                   (rf/dispatch [:show-snackbar {:text "Failed to create plugin"
                                                 :level :error
                                                 :details error}]))]
     {:fx [[:dispatch [:fetch {:method "POST"
                               :uri "/plugins"
                               :body body
                               :on-success success
                               :on-failure failure}]]]})))

(rf/reg-event-fx
 :plugins->update-plugin
 (fn
   [{:keys [db]} [_ plugin]]
   (let [on-success (fn []
                      (rf/dispatch [:show-snackbar {:level :success
                                                    :text "Plugin updated"}])
                      (rf/dispatch [:close-modal])
                      (rf/dispatch [:plugins->get-plugin-by-name (:name plugin)]))
         on-failure (fn [error]
                      (rf/dispatch [:show-snackbar {:level :error
                                                    :text "Failed to update plugin"
                                                    :details error}]))]
     {:fx [[:dispatch [:fetch {:method "PUT"
                               :uri (str "/plugins/" (:name plugin))
                               :body plugin
                               :on-failure on-failure
                               :on-success on-success}]]]})))
(rf/reg-event-fx
 :plugins->navigate->manage-plugin
 (fn
   [_ [_ plugin-name]]
   {:fx [[:dispatch [:plugins->get-plugin-by-name plugin-name]]
         [:dispatch [:navigate :manage-plugin {} :plugin-name plugin-name]]]}))

