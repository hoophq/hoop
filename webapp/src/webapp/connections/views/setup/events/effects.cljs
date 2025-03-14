(ns webapp.connections.views.setup.events.effects
  (:require
   [re-frame.core :as rf]
   [webapp.connections.views.setup.events.process-form :as process-form]))

;; Initialize app state
(rf/reg-event-db
 :connection-setup/initialize-state
 (fn [db [_ initial-data]]
   (if initial-data
     (assoc db :connection-setup initial-data)
     (assoc db :connection-setup {}))))

;; Main effects that change multiple parts of state or interact with external systems
(rf/reg-event-fx
 :connection-setup/select-type
 (fn [{:keys [db]} [_ connection-type]]
   {:db (assoc-in db [:connection-setup :type] connection-type)
    :fx [[:dispatch [:connection-setup/next-step :credentials]]]}))

(rf/reg-event-fx
 :connection-setup/submit
 (fn [{:keys [db]} _]
   (let [;; Valores atuais de credenciais
         current-env-key (get-in db [:connection-setup :credentials :current-key])
         current-env-value (get-in db [:connection-setup :credentials :current-value])
         current-file-name (get-in db [:connection-setup :credentials :current-file-name])
         current-file-content (get-in db [:connection-setup :credentials :current-file-content])

         ;; Valores atuais de tags
         current-tag-key (get-in db [:connection-setup :tags :current-key])
         current-tag-value (get-in db [:connection-setup :tags :current-value])

         ;; Primeiro processa as credenciais atuais
         db-with-current-creds (cond-> db
                                 ;; Incluir credenciais de ambiente atuais
                                 (and (not (empty? current-env-key))
                                      (not (empty? current-env-value)))
                                 (update-in [:connection-setup :credentials :environment-variables]
                                            #(conj (or % []) {:key current-env-key :value current-env-value}))

                                 ;; Incluir credenciais de arquivo atuais
                                 (and (not (empty? current-file-name))
                                      (not (empty? current-file-content)))
                                 (update-in [:connection-setup :credentials :configuration-files]
                                            #(conj (or % []) {:key current-file-name :value current-file-content})))

         ;; Depois processa a tag atual (se existir e ainda não tiver sido adicionada)
         db-with-current-tag (cond-> db-with-current-creds
                               ;; Incluir tag atual - armazenando apenas os valores, não objetos
                               (and current-tag-key (.-value current-tag-key))
                               (update-in [:connection-setup :tags :data]
                                          #(conj (or % [])
                                                 {:key (.-value current-tag-key)
                                                  :value (if current-tag-value
                                                           (.-value current-tag-value)
                                                           "")})))

         ;; Processo especial para o payload: mover as tags de :data para o nível superior
         tags-data (get-in db-with-current-tag [:connection-setup :tags :data] [])
         db-with-processed-tags (assoc-in db-with-current-tag [:connection-setup :tags] tags-data)

         ;; Gerar o payload
         payload (process-form/process-payload db-with-processed-tags)]

     ;; Despachar para criar a conexão e reinicializar o estado
     {:fx [[:dispatch [:connections->create-connection payload]]
           [:dispatch-later {:ms 500
                             :dispatch [:connection-setup/initialize-state nil]}]]})))
