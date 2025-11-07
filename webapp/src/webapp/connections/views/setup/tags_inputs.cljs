(ns webapp.connections.views.setup.tags-inputs
  (:require
   ["@radix-ui/themes" :refer [Box Button Grid Heading Text]]
   ["lucide-react" :refer [Plus]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.multiselect :as multiselect]
   [clojure.string :as str]
   [webapp.connections.views.setup.tags-utils :as tags-utils]))

;; Componente de select para chave
(defn key-select-component []
  (let [current-key @(rf/subscribe [:connection-setup/current-key])
        key-options @(rf/subscribe [:connection-tags/key-options])
        loading? @(rf/subscribe [:connection-tags/loading?])
        validation-error @(rf/subscribe [:connection-setup/key-validation-error])]
    [:div
     [multiselect/single-creatable-grouped
      {:default-value current-key
       :options key-options
       :label "Key"
       :disabled? loading?
       :on-change (fn [selected-option]
                    (rf/dispatch [:connection-setup/set-current-key selected-option]))
       :on-create-option (fn [input-value]
                           ;; Validação: permite apenas letras, números e hifens
                           (if (re-matches #"^[a-zA-Z0-9\-]+$" input-value)
                             (let [full-key input-value
                                   new-option #js{:value full-key
                                                  :label input-value}]
                               (rf/dispatch [:connection-setup/set-current-key new-option])
                               (rf/dispatch [:connection-setup/set-key-validation-error nil]))
                             ;; Exibir erro por 3 segundos
                             (do
                               (rf/dispatch [:connection-setup/set-key-validation-error
                                             "Only letters, numbers and hyphens are allowed"])
                               (js/setTimeout
                                #(rf/dispatch [:connection-setup/set-key-validation-error nil])
                                3000))))
       :placeholder (if loading?
                      "Loading options..."
                      "Select or create a key...")}]
     ;; Mensagem de erro condicional
     (when validation-error
       [:div {:class "text-red-500 text-sm mt-1"} validation-error])]))

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
(defn tag-item [index {:keys [key label value]}]
  (let [key-options @(rf/subscribe [:connection-tags/key-options])
        available-values @(rf/subscribe [:connection-setup/available-values-for-index index])
        key-obj (when key #js{:value key
                              :label (tags-utils/extract-label key)})
        value-obj (when value #js{:value value :label value})]
    ^{:key (str "tag-" index)}
    [:<>
     ;; Select para Key
     [multiselect/single-creatable-grouped
      {:key (str "key-" index)
       :default-value key-obj
       :value key-obj
       :options key-options
       :label "Key"
       :disabled? false
       :on-change (fn [selected-option]
                    (rf/dispatch [:connection-setup/update-tag-key
                                  index
                                  selected-option]))
       :on-create-option (fn [input-value]
                           (let [new-option #js{:value input-value
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
       :label "Value"
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
       [:> Grid {:columns "2" :gap "4"}
        (doall
         (map-indexed tag-item tags))]])))

;; Componente principal refatorado
(defn main []
  ;; Usando r/with-let para inicializar apenas uma vez
  (r/with-let [_ (rf/dispatch-sync [:connection-tags/fetch])]
    (fn []
      [:> Box {:class "space-y-6"}
       [:> Box
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "Tags"]
        [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
         "Add custom labels to manage and track resource roles."]]

       ;; Lista de tags existentes como componente separado
       [:> Box
        [existing-tags]

        ;; Inputs substituídos por selects em componentes separados
        [:> Grid {:columns "2" :gap "4" :my "4"}
         [:> Box [key-select-component]]
         [:> Box [value-select-component]]]

        ;; Botão Add como componente separado
        [add-tag-button]]])))
