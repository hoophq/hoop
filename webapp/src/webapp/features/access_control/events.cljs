(ns webapp.features.access-control.events
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :access-control/activate
 (fn [{:keys [db]} _]
   {:fx [[:dispatch [:plugins->create-plugin {:name "access_control"
                                              :connections []}]]
         [:dispatch [:show-snackbar {:level :success
                                     :text "Access Control activated successfully!"}]]]}))

(rf/reg-event-fx
 :access-control/add-group-permissions
 (fn [{:keys [db]} [_ {:keys [group-id connections plugin]}]]

   (let [plugin-connections (or (:connections plugin) [])

         selected-connection-ids (set (map :id (or connections [])))

         final-connections
         (if (and (empty? plugin-connections) (seq selected-connection-ids))
           (mapv (fn [conn]
                   {:id (:id conn)
                    :name (:name conn)
                    :config [group-id]})
                 connections)

           (let [connections-with-group-removed
                 (map (fn [conn]
                        (if (and (:config conn) (some #(= % group-id) (:config conn)))
                          (update conn :config (fn [config] (filter #(not= % group-id) config)))
                          conn))
                      plugin-connections)


                 final-existing-connections
                 (map (fn [conn]
                        (if (contains? selected-connection-ids (:id conn))
                          (update conn :config (fn [config] (distinct (conj (or config []) group-id))))
                          conn))
                      connections-with-group-removed)
                 existing-ids (set (map :id connections-with-group-removed))
                 new-connections (filter #(not (contains? existing-ids (:id %))) connections)
                 new-connection-objects (map (fn [conn]
                                               {:id (:id conn)
                                                :name (:name conn)
                                                :config [group-id]})
                                             new-connections)]

             (vec (concat final-existing-connections new-connection-objects))))


         new-plugin-data (assoc plugin :connections final-connections)]


     {:fx [[:dispatch [:plugins->update-plugin new-plugin-data]]
           [:dispatch [:show-snackbar {:level :success
                                       :text "Group permissions updated successfully!"}]]]})))


(rf/reg-event-fx
 :access-control/delete-group
 (fn [{:keys [db]} [_ group-name]]
   {:fx [[:dispatch [:fetch {:method "DELETE"
                             :uri (str "/users/groups/" group-name)
                             :on-success (fn []
                                           (rf/dispatch [:show-snackbar {:level :success
                                                                         :text (str "Group '" group-name "' deleted successfully!")}])
                                           (rf/dispatch [:users->get-user-groups]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:show-snackbar {:level :error
                                                                         :text (str "Failed to delete group: "
                                                                                    (or (get-in error [:response :message])
                                                                                        "Unknown error"))}]))}]]]}))

(rf/reg-event-fx
 :access-control/create-group-with-permissions
 (fn [{:keys [db]} [_ {:keys [name description connections]}]]
   {:fx [[:dispatch [:fetch {:method "POST"
                             :uri "/users/groups"
                             :body {:name name
                                    :description description}
                             :on-success (fn [_]
                                           (rf/dispatch [:show-snackbar {:level :success
                                                                         :text (str "Group '" name "' created successfully!")}])
                                           (rf/dispatch [:users->get-user-groups])

                                           (when (seq connections)
                                             (rf/dispatch [:plugins->get-plugin-by-name-with-callback "access_control"
                                                           {:on-success (fn [plugin]
                                                                          (rf/dispatch [:access-control/add-group-permissions
                                                                                        {:group-id name
                                                                                         :connections connections
                                                                                         :plugin plugin}]))}]))

                                           (js/setTimeout #(rf/dispatch [:navigate :access-control]) 1000))
                             :on-failure (fn [error]
                                           (rf/dispatch [:show-snackbar {:level :error
                                                                         :text (str "Failed to create group: "
                                                                                    (or (get-in error [:response :message])
                                                                                        "Unknown error"))}]))}]]]}))

(rf/reg-event-fx
 :plugins->get-plugin-by-name-with-callback
 (fn [{:keys [db]} [_ plugin-name {:keys [on-success]}]]
   {:fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/plugins/" plugin-name)
                             :on-success (fn [response]
                                           (let [plugin (merge response {:installed? true})]
                                             (rf/dispatch [:plugins->set-plugin plugin])
                                             (when on-success
                                               (on-success plugin))))
                             :on-failure #(rf/dispatch [:plugins->set-plugin {:name plugin-name
                                                                              :installed? false}])}]]]
    :db (assoc-in db [:plugins->plugin-details :status] :loading)}))

(rf/reg-event-fx
 :plugins->delete-plugin
 (fn [{:keys [db]} [_ plugin-name]]
   {:fx [[:dispatch [:fetch {:method "DELETE"
                             :uri (str "/plugins/" plugin-name)
                             :on-success (fn []
                                           (rf/dispatch [:show-snackbar {:level :success
                                                                         :text "Access control disabled successfully!"}])
                                           (rf/dispatch [:plugins->get-plugin-by-name plugin-name]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:show-snackbar {:level :error
                                                                         :text (str "Failed to disable access control: "
                                                                                    (or (get-in error [:response :message])
                                                                                        "Unknown error"))}]))}]]]}))
