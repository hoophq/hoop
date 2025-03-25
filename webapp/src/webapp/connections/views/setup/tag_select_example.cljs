(ns webapp.connections.views.setup.tag-select-example
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.multiselect :as multiselect]
   [webapp.connections.views.setup.tags-utils :as tags-utils]))

;; Event handler para buscar tags da API
(rf/reg-event-fx
 :connection-tags/fetch
 (fn [{:keys [db]} _]
   ;; Numa implementação real, isso seria uma chamada à API
   ;; Por enquanto, usamos os dados de mock
   {:dispatch [:connection-tags/set tags-utils/mock-tags-data]}))

;; Armazenar tags no app-db
(rf/reg-event-db
 :connection-tags/set
 (fn [db [_ tags-data]]
   (assoc-in db [:connection-tags :data] tags-data)))

;; Subscription para acessar os dados de tags
(rf/reg-sub
 :connection-tags/data
 (fn [db]
   (get-in db [:connection-tags :data])))

(defn two-select-component
  "Um componente de exemplo que usa dois selects: um para chaves e outro para valores"
  []
  (let [;; Estado local
        selected-key (r/atom nil)
        selected-value (r/atom nil)
        tags-data (rf/subscribe [:connection-tags/data])

        ;; Formatar as opções para o primeiro select (keys)
        key-options (r/atom [])

        ;; Formatar valores disponíveis para o segundo select baseado na key selecionada
        available-values (r/atom [])]

    ;; Buscar tags ao montar o componente
    (r/create-class
     {:component-did-mount
      (fn [_]
        (rf/dispatch [:connection-tags/fetch]))

      :component-did-update
      (fn [_ _]
        ;; Atualizar as opções quando os dados de tags mudarem
        (when @tags-data
          (reset! key-options (:grouped-options (tags-utils/format-keys-for-select @tags-data)))))

      :reagent-render
      (fn []
        [:> Box {:class "space-y-4 p-4"}
         [:> Heading {:as "h3" :size "4" :mb "2"}
          "Select Tag"]

         [:> Flex {:direction "column" :gap "4" :mb "4"}
          ;; Primeiro select: para escolher a chave (key)
          [:> Box
           [:> Text {:as "p" :size "2" :mb "1" :weight "medium"} "Key"]
           [multiselect/single-creatable-grouped
            {:default-value @selected-key
             :options @key-options
             :on-change (fn [selected-option]
                          (reset! selected-key selected-option)
                          (when (and selected-option @tags-data)
                            ;; Atualizar valores disponíveis baseado na key selecionada
                            (let [key-name (.-value selected-option)
                                  values (tags-utils/get-values-for-key @tags-data key-name)]
                              (reset! available-values values)
                              (reset! selected-value nil))))
             :on-create-option (fn [input-value]
                                 (let [new-option #js{:value input-value
                                                      :label (tags-utils/format-label input-value)}]
                                   (reset! selected-key new-option)
                                   (reset! available-values [])))
             :placeholder "Select or create a key..."
             :label "Tag Key"}]]

          ;; Segundo select: para escolher o valor
          [:> Box
           [:> Text {:as "p" :size "2" :mb "1" :weight "medium"} "Value"]
           [multiselect/single-creatable-grouped
            {:default-value @selected-value
             :options @available-values
             :disabled? (nil? @selected-key)
             :on-change (fn [selected-option]
                          (reset! selected-value selected-option))
             :on-create-option (fn [input-value]
                                 (let [new-option #js{:value input-value :label input-value}]
                                   (reset! selected-value new-option)))
             :placeholder (if @selected-key
                            "Select or create a value..."
                            "First select a key...")
             :label "Tag Value"}]]

          ;; Exibir a seleção atual
          (when (and @selected-key @selected-value)
            [:> Box {:class "p-3 bg-gray-100 rounded-md mt-4"}
             [:> Text {:size "2" :weight "medium"}
              (str "Selected: "
                   (if (.-label @selected-key) (.-label @selected-key) "")
                   " = "
                   (if (.-label @selected-value) (.-label @selected-value) ""))]])]

         ;; Instruções de uso
         [:> Box {:class "border-t border-gray-200 pt-4 mt-4"}
          [:> Text {:size "2" :color "gray"}
           "Select a key from the first dropdown, then select or create a value in the second dropdown. "
           "The keys are grouped by category (Infrastructure, Security, etc.)."]]])})))

(defn main
  "Componente principal para o exemplo de seleção de tags"
  []
  [two-select-component])
