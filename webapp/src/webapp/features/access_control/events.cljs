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
