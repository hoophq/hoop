(ns webapp.connections.views.setup.tags-inputs
  (:require
   ["@radix-ui/themes" :refer [Box Button Grid Flex Heading Text]]
   ["lucide-react" :refer [X Plus]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multiselect]
   [clojure.string :as str]
   [webapp.connections.views.setup.tags-utils :as tags-utils]))

;; Subscription para buscar os dados de tags agrupados
(rf/reg-event-fx
 :connection-tags/fetch
 (fn [{:keys [db]} _]
   ;; Numa implementação real, isso seria uma chamada à API
   ;; Por enquanto, usamos os dados de mock
   {:db (assoc-in db [:connection-tags :loading?] true)
    :dispatch [:connection-tags/set tags-utils/mock-tags-data]}))

;; Armazenar tags no app-db
(rf/reg-event-db
 :connection-tags/set
 (fn [db [_ tags-data]]
   (-> db
       (assoc-in [:connection-tags :data] tags-data)
       (assoc-in [:connection-tags :loading?] false))))

;; Subscription para acessar os dados de tags
(rf/reg-sub
 :connection-tags/data
 (fn [db]
   (get-in db [:connection-tags :data])))

;; Subscription para verificar se as tags estão carregando
(rf/reg-sub
 :connection-tags/loading?
 (fn [db]
   (get-in db [:connection-tags :loading?] true)))

;; Subscription para obter as chaves formatadas para o select
(rf/reg-sub
 :connection-tags/key-options
 :<- [:connection-tags/data]
 (fn [tags-data]
   (when tags-data
     (:grouped-options (tags-utils/format-keys-for-select tags-data)))))

;; Eventos e subscriptions para current-key e current-value
;; -------------------------------------------------------

;; Armazenar a chave selecionada e atualizar valores disponíveis em uma única operação
(rf/reg-event-fx
 :connection-setup/set-current-key
 (fn [{:keys [db]} [_ current-key]]
   (let [full-key (when current-key (.-value current-key))
         subcategory (when full-key (tags-utils/extract-subcategory full-key))
         tags-data (get-in db [:connection-tags :data])
         available-values (when (and full-key tags-data)
                            (tags-utils/get-values-for-key tags-data full-key))]
     {:db (-> db
              (assoc-in [:connection-setup :tags :current-key] current-key)
              (assoc-in [:connection-setup :tags :current-full-key] full-key)
              (assoc-in [:connection-setup :tags :current-subcategory] subcategory)
              (assoc-in [:connection-setup :tags :available-values] (or available-values []))
              (assoc-in [:connection-setup :tags :current-value] nil))})))

;; Armazenar o valor selecionado no app-db
(rf/reg-event-db
 :connection-setup/set-current-value
 (fn [db [_ current-value]]
   (assoc-in db [:connection-setup :tags :current-value] current-value)))

;; Subscription para acessar a chave selecionada
(rf/reg-sub
 :connection-setup/current-key
 (fn [db]
   (get-in db [:connection-setup :tags :current-key])))

;; Subscription para acessar o valor selecionado
(rf/reg-sub
 :connection-setup/current-value
 (fn [db]
   (get-in db [:connection-setup :tags :current-value])))

;; Limpar os estados iniciais do app-db para garantir que não existam tags indesejadas
(rf/reg-event-db
 :connection-setup/initialize-tags
 (fn [db _]
   (-> db
       (assoc-in [:connection-setup :tags :data] [])
       (assoc-in [:connection-setup :tags :current-key] nil)
       (assoc-in [:connection-setup :tags :current-full-key] nil)
       (assoc-in [:connection-setup :tags :current-subcategory] nil)
       (assoc-in [:connection-setup :tags :current-value] nil)
       (assoc-in [:connection-setup :tags :available-values] []))))

;; Evento para limpar os valores selecionados após adicionar uma tag
(rf/reg-event-db
 :connection-setup/clear-current-tag
 (fn [db _]
   (-> db
       (assoc-in [:connection-setup :tags :current-key] nil)
       (assoc-in [:connection-setup :tags :current-full-key] nil)
       (assoc-in [:connection-setup :tags :current-subcategory] nil)
       (assoc-in [:connection-setup :tags :current-value] nil))))

;; Subscription para acessar valores disponíveis para o segundo select
(rf/reg-sub
 :connection-setup/available-values
 (fn [db]
   (get-in db [:connection-setup :tags :available-values] [])))

;; Evento para adicionar uma tag à lista
(rf/reg-event-db
 :connection-setup/add-tag
 (fn [db [_ full-key value]]
   (let [subcategory (tags-utils/extract-subcategory full-key)]
     (if (and full-key (not (str/blank? value)))
       (update-in db [:connection-setup :tags :data]
                  #(conj (or % []) {:key full-key
                                    :subcategory subcategory
                                    :value value}))
       db))))

;; Subscription para acessar as tags adicionadas
(rf/reg-sub
 :connection-setup/tags
 (fn [db]
   (get-in db [:connection-setup :tags :data] [])))

;; Subscription para obter as tags formatadas para o backend
(rf/reg-sub
 :connection-setup/tags-for-backend
 :<- [:connection-setup/tags]
 (fn [tags]
   (tags-utils/prepare-tags-for-backend tags)))

;; Evento para atualizar uma tag existente
(rf/reg-event-fx
 :connection-setup/update-tag-key
 (fn [{:keys [db]} [_ index selected-option]]
   (let [full-key (when selected-option (.-value selected-option))
         subcategory (when full-key (tags-utils/extract-subcategory full-key))
         tags-data (get-in db [:connection-tags :data])
         available-values (when (and full-key tags-data)
                            (tags-utils/get-values-for-key tags-data full-key))]
     {:db (-> db
              (assoc-in [:connection-setup :tags :data index :key] full-key)
              (assoc-in [:connection-setup :tags :data index :subcategory] subcategory)
              (assoc-in [:connection-setup :tags :data index :value] nil)
              ;; Armazenar os valores disponíveis para esta tag específica
              (assoc-in [:connection-setup :tags :available-values-for-index index] (or available-values [])))})))

;; Evento para atualizar o valor de uma tag existente
(rf/reg-event-db
 :connection-setup/update-tag-value
 (fn [db [_ index selected-option]]
   (let [value (when selected-option (.-value selected-option))]
     (assoc-in db [:connection-setup :tags :data index :value] value))))

;; Subscription para obter valores disponíveis para uma tag específica
(rf/reg-sub
 :connection-setup/available-values-for-index
 (fn [db [_ index]]
   (get-in db [:connection-setup :tags :available-values-for-index index] [])))

;; Evento para remover uma tag
(rf/reg-event-db
 :connection-setup/remove-tag
 (fn [db [_ index]]
   (update-in db [:connection-setup :tags :data]
              (fn [tags]
                (vec (concat (subvec tags 0 index)
                             (subvec tags (inc index))))))))

;; Componente de select para chave
(defn key-select-component []
  (let [current-key @(rf/subscribe [:connection-setup/current-key])
        key-options @(rf/subscribe [:connection-tags/key-options])
        loading? @(rf/subscribe [:connection-tags/loading?])]
    [multiselect/single-creatable-grouped
     {:default-value current-key
      :options key-options
      :label "Key"
      :disabled? loading?
      :on-change (fn [selected-option]
                   (rf/dispatch [:connection-setup/set-current-key selected-option]))
      :on-create-option (fn [input-value]
                          (let [full-key (str "custom." input-value)
                                new-option #js{:value full-key
                                               :label input-value}]
                            (rf/dispatch [:connection-setup/set-current-key new-option])))
      :placeholder (if loading?
                     "Loading options..."
                     "Select or create a key...")}]))

;; Componente de select para valor
(defn value-select-component []
  (let [current-key @(rf/subscribe [:connection-setup/current-key])
        current-value @(rf/subscribe [:connection-setup/current-value])
        available-values @(rf/subscribe [:connection-setup/available-values])
        loading? @(rf/subscribe [:connection-tags/loading?])]
    [multiselect/single-creatable-grouped
     {:default-value current-value
      :options available-values
      :label "Value"
      :disabled? (or (nil? current-key) loading?)
      :on-change (fn [selected-option]
                   (rf/dispatch [:connection-setup/set-current-value selected-option]))
      :on-create-option (fn [input-value]
                          (let [new-option #js{:value input-value :label input-value}]
                            (rf/dispatch [:connection-setup/set-current-value new-option])))
      :placeholder (cond
                     loading? "Loading options..."
                     (nil? current-key) "First select a key..."
                     :else "Select or create a value...")}]))

;; Componente para o botão de adicionar tag
(defn add-tag-button []
  (let [current-key @(rf/subscribe [:connection-setup/current-key])
        current-value @(rf/subscribe [:connection-setup/current-value])
        loading? @(rf/subscribe [:connection-tags/loading?])
        key-value (when current-key (.-value current-key))
        value-value (when current-value (.-value current-value))
        disabled? (or (nil? current-key)
                      loading?
                      (nil? key-value)
                      (str/blank? key-value)
                      (and current-value (str/blank? value-value)))]
    [:> Button {:size "2"
                :variant "soft"
                :type "button"
                :disabled disabled?
                :on-click (fn []
                            (when (and current-key (not disabled?))
                              (let [full-key (.-value current-key)
                                    value-val (if current-value
                                                (.-value current-value)
                                                "")]
                                (when (and (not (str/blank? full-key))
                                           (not (str/blank? value-val)))
                                  (rf/dispatch [:connection-setup/add-tag
                                                full-key
                                                value-val])
                                  ;; Limpar os valores após adicionar
                                  (rf/dispatch [:connection-setup/clear-current-tag])))))}
     [:> Plus {:size 16}]
     "Add"]))

;; Componente para tag individual
(defn tag-item [index {:keys [key subcategory value]}]
  (let [key-options @(rf/subscribe [:connection-tags/key-options])
        available-values @(rf/subscribe [:connection-setup/available-values-for-index index])
        key-obj (when key #js{:value key
                              :label (or (tags-utils/tag-key-to-display-name key) subcategory)})
        value-obj (when value #js{:value value :label value})]
    [:<>
     ;; Select para Key
     [multiselect/single-creatable-grouped
      {:key (str "key-" index)
       :default-value key-obj
       :value key-obj
       :options key-options
       :label (str "Key " (inc index))
       :disabled? false
       :on-change (fn [selected-option]
                    (rf/dispatch [:connection-setup/update-tag-key
                                  index
                                  selected-option]))
       :on-create-option (fn [input-value]
                           (let [new-option #js{:value (str "custom." input-value)
                                                :label input-value}]
                             (rf/dispatch [:connection-setup/update-tag-key
                                           index
                                           new-option])))
       :placeholder "Select or create a key..."}]

     ;; Select para Value
     [multiselect/single-creatable-grouped
      {:key (str "value-" index)
       :default-value value-obj
       :value value-obj
       :options available-values
       :label (str "Value " (inc index))
       :disabled? false
       :on-change (fn [selected-option]
                    (rf/dispatch [:connection-setup/update-tag-value
                                  index
                                  selected-option]))
       :on-create-option (fn [input-value]
                           (let [new-option #js{:value input-value :label input-value}]
                             (rf/dispatch [:connection-setup/update-tag-value
                                           index
                                           new-option])))
       :placeholder "Select or create a value..."}]]))

;; Componente para exibir tags existentes
(defn existing-tags []
  (let [tags @(rf/subscribe [:connection-setup/tags])]
    (when (seq tags)
      [:> Box {:class "space-y-4"}
       [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"}
        "Existing Tags"]
       [:> Grid {:columns "2" :gap "2"}
        (map-indexed tag-item tags)]])))

;; Componente principal refatorado
(defn main []
  ;; Usando r/with-let para inicializar apenas uma vez
  (r/with-let [_ (do
                   (rf/dispatch-sync [:connection-setup/initialize-tags])
                   (rf/dispatch-sync [:connection-tags/fetch]))]
    (fn []
      [:> Box {:class "space-y-4"}
       [:> Box
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "Tags"]
        [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
         "Add custom labels to manage and track connections."]]

       ;; Lista de tags existentes como componente separado
       [existing-tags]

       ;; Inputs substituídos por selects em componentes separados
       [:> Grid {:columns "2" :gap "2"}
        [:> Box [key-select-component]]
        [:> Box [value-select-component]]]

       ;; Botão Add como componente separado
       [add-tag-button]])))
