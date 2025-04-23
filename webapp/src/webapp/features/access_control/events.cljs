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
   (let [;; Obter o plugin inteiro
         plugin-connections (:connections plugin)

         ;; Lista de IDs das conexões selecionadas
         selected-connection-ids (set (map :id connections))

         ;; Primeiro, remover o grupo atual de todas as conexões
         connections-with-group-removed
         (map (fn [conn]
                (if (some #(= % group-id) (:config conn))
                  ;; Se a conexão contém o grupo, remove-o
                  (update conn :config (fn [config] (filter #(not= % group-id) config)))
                  ;; Caso contrário, mantém a conexão como está
                  conn))
              plugin-connections)

         ;; Agora, adicionar o grupo às conexões selecionadas
         final-connections
         (map (fn [conn]
                (if (contains? selected-connection-ids (:id conn))
                  ;; Se a conexão foi selecionada, adiciona o grupo a ela
                  (update conn :config (fn [config] (distinct (conj (or config []) group-id))))
                  ;; Caso contrário, mantém a conexão como está
                  conn))
              connections-with-group-removed)

         ;; Atualizar o plugin com as novas conexões
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
