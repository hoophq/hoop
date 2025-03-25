(ns webapp.connections.views.setup.events.effects
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.connections.views.setup.events.process-form :as process-form]
   [webapp.connections.views.setup.tags-utils :as tags-utils]))

;; Initialize app state
(rf/reg-event-db
 :connection-setup/initialize-state
 (fn [db [_ initial-data]]
   (if initial-data
     (assoc db :connection-setup initial-data)
     (assoc db :connection-setup {}))))

;; Função para filtrar tags inválidas (com key ou value vazios)
(defn filter-valid-tags
  "Remove tags que possuem key ou value vazios"
  [tags]
  (filterv (fn [{:keys [key value]}]
             (and key
                  (not (str/blank? (if (string? key) key (str key))))
                  value
                  (not (str/blank? (if (string? value) value (str value))))))
           tags))

;; Main effects that change multiple parts of state or interact with external systems
(rf/reg-event-fx
 :connection-setup/select-type
 (fn [{:keys [db]} [_ connection-type]]
   {:db (assoc-in db [:connection-setup :type] connection-type)
    :fx [[:dispatch [:connection-setup/next-step :credentials]]]}))

(rf/reg-event-fx
 :connection-tags/fetch
 (fn [{:keys [db]} _]
   ;; Numa implementação real, isso seria uma chamada à API
   ;; Por enquanto, usamos os dados de mock
   {:fx [[:dispatch [:fetch {:method "GET"
                             :uri "/connection-tags"
                             :on-success (fn [tags]
                                           (rf/dispatch [:connection-tags/set tags]))}]]]
    :db (assoc-in db [:connection-tags :loading?] true)}))

(rf/reg-event-fx
 :connection-setup/set-current-key
 (fn [{:keys [db]} [_ current-key]]
   (let [full-key (when current-key (.-value current-key))
         label (when full-key
                 (tags-utils/extract-label full-key))
         tags-data (get-in db [:connection-tags :data])
         available-values (when (and full-key tags-data)
                            (tags-utils/get-values-for-key tags-data full-key))]
     {:db (-> db
              (assoc-in [:connection-setup :tags :current-key] current-key)
              (assoc-in [:connection-setup :tags :current-full-key] full-key)
              (assoc-in [:connection-setup :tags :current-label] label)
              (assoc-in [:connection-setup :tags :available-values] (or available-values []))
              (assoc-in [:connection-setup :tags :current-value] nil))})))

(rf/reg-event-fx
 :connection-setup/update-tag-key
 (fn [{:keys [db]} [_ index selected-option]]
   (let [full-key (when selected-option (.-value selected-option))
         label (when full-key
                 (tags-utils/extract-label full-key))
         tags-data (get-in db [:connection-tags :data])
         available-values (when (and full-key
                                     (not (str/blank? full-key))
                                     tags-data)
                            (tags-utils/get-values-for-key tags-data full-key))]
     {:db (-> db
              (assoc-in [:connection-setup :tags :data index :key] full-key)
              (assoc-in [:connection-setup :tags :data index :label] label)
              (assoc-in [:connection-setup :tags :data index :value] nil)
              ;; Armazenar os valores disponíveis para esta tag específica
              (assoc-in [:connection-setup :tags :available-values-for-index index] (or available-values [])))})))

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

         ;; Filtra tags inválidas (com key ou value vazios)
         existing-tags (get-in db-with-current-tag [:connection-setup :tags :data] [])
         valid-tags (filter-valid-tags existing-tags)

         ;; Atualiza db com apenas as tags válidas
         db-with-processed-tags (assoc-in db-with-current-tag [:connection-setup :tags :data] valid-tags)

         ;; Gerar o payload
         payload (process-form/process-payload db-with-processed-tags)]

     ;; Despachar para criar a conexão e reinicializar o estado
     {:fx [[:dispatch [:connections->create-connection payload]]
           [:dispatch-later {:ms 500
                             :dispatch [:connection-setup/initialize-state nil]}]]})))
