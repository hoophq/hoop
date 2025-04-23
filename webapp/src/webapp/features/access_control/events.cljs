(ns webapp.features.access-control.events
  (:require
   [re-frame.core :as rf]))

;; Nesta primeira fase, vamos apenas reutilizar os eventos existentes para plugins
;; Posteriormente podemos adicionar eventos específicos para a feature

(rf/reg-event-fx
 :access-control/init
 (fn [{:keys [db]} _]
   {:fx [[:dispatch [:plugins->get-plugin-by-name "access_control"]]
         [:dispatch [:users->get-user-groups]]]}))

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
   (let [connection-configs (map (fn [conn]
                                   {:id (:id conn)
                                    :config [group-id]})
                                 connections)
         current-connections (:connections plugin)

         ;; Filtrar conexões que já têm este grupo configurado
         existing-connections (filter #(some? (first (filter (fn [conn]
                                                               (= (:id conn) (:id %)))
                                                             current-connections)))
                                      connection-configs)

         ;; Atualizar conexões existentes para incluir o novo grupo
         updated-connections (map (fn [conn]
                                    (let [existing (first (filter #(= (:id %) (:id conn)) current-connections))
                                          updated-config (if existing
                                                           (distinct (concat (:config existing) (:config conn)))
                                                           (:config conn))]
                                      (assoc conn :config updated-config)))
                                  existing-connections)

         ;; Conexões que não existem ainda no plugin
         new-connections (filter #(not (some? (first (filter (fn [conn]
                                                               (= (:id conn) (:id %)))
                                                             current-connections))))
                                 connection-configs)

         ;; Mesclar conexões atualizadas e novas com as que não foram afetadas
         final-connections (concat
                            (filter #(not (some? (first (filter (fn [conn]
                                                                  (= (:id conn) (:id %)))
                                                                existing-connections))))
                                    current-connections)
                            updated-connections
                            new-connections)

         ;; Atualizar plugin com as novas configurações
         new-plugin-data (assoc plugin :connections final-connections)]

     {:fx [[:dispatch [:plugins->update-plugin new-plugin-data]]
           [:dispatch [:show-snackbar {:level :success
                                       :text "Group permissions updated successfully!"}]]]})))

(rf/reg-event-fx
 :access-control/create-group
 (fn [{:keys [db]} [_ group-name]]
   {:fx [[:dispatch [:fetch {:method "POST"
                             :uri "/users/groups"
                             :body {:name group-name}
                             :on-success (fn []
                                           (rf/dispatch [:show-snackbar {:level :success
                                                                         :text (str "Group '" group-name "' created successfully!")}])
                                           (rf/dispatch [:users->get-user-groups])
                                           (rf/dispatch [:navigate :access-control]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:show-snackbar {:level :error
                                                                         :text (str "Failed to create group: "
                                                                                    (or (get-in error [:response :message])
                                                                                        "Unknown error"))}]))}]]]}))

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
                             :on-success (fn [response]
                                           ;; Após criar o grupo, configurar permissões
                                           (rf/dispatch [:show-snackbar {:level :success
                                                                         :text (str "Group '" name "' created successfully!")}])
                                           (rf/dispatch [:users->get-user-groups])

                                           ;; Se houver conexões selecionadas, configurar permissões
                                           (when (seq connections)
                                             (rf/dispatch [:plugins->get-plugin-by-name-with-callback "access_control"
                                                           {:on-success (fn [plugin]
                                                                          (rf/dispatch [:access-control/add-group-permissions
                                                                                        {:group-id name
                                                                                         :connections connections
                                                                                         :plugin plugin}]))}]))

                                           ;; Redirecionar para a lista
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
