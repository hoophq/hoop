(ns webapp.connections.views.setup.tags-inputs
  (:require
   ["@radix-ui/themes" :refer [Box Button Grid Flex Heading Text]]
   ["lucide-react" :refer [X Plus]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn main []
  (let [current-key (r/atom "")
        current-value (r/atom "")
        tags (rf/subscribe [:connection-setup/tags])]
    (fn []
      [:> Box {:class "space-y-4"}
       [:> Box
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "Tags"]
        [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
         "Add custom labels to manage and track connections."]]

       ;; Lista de tags existentes
       (when (seq @tags)
         [:> Grid {:columns "2" :gap "2"}
          (for [{:keys [key value]} @tags]
            ^{:key key}
            [:<>
             [forms/input
              {:label "Key"
               :value key
               :disabled true}]
             [forms/input
              {:label "Value"
               :value value
               :type "password"
               :disabled true}]])])

       ;; Inputs para nova tag
       [:> Grid {:columns "2" :gap "2"}
        [forms/input
         {:label "Key"
          :size "3"
          :placeholder "Key"
          :value @current-key
          :not-margin-bottom? true
          :on-change #(reset! current-key (-> % .-target .-value))}]
        [forms/input
         {:label "Value (Optional)"
          :size "3"
          :placeholder "Value"
          :value @current-value
          :not-margin-bottom? true
          :on-change #(reset! current-value (-> % .-target .-value))}]]

       ;; Botão de adicionar
       [:> Button {:size "2"
                   :variant "soft"
                   :type "button"
                   :on-click (fn []
                               (when (not (empty? @current-key))
                                 (rf/dispatch [:connection-setup/add-tag
                                               @current-key
                                               @current-value])
                                 (reset! current-key "")
                                 (reset! current-value "")))}
        [:> Plus {:size 16}]
        "Add"]])))
