(ns webapp.webclient.components.panels.metadata
  (:require
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   ["@radix-ui/themes" :refer [Box Button Flex Switch Text]]))

(defn main []
  (let [keep-metadata? (rf/subscribe [:editor-plugin/keep-metadata?])
        metadata (rf/subscribe [:editor-plugin/metadata])
        metadata-key (rf/subscribe [:editor-plugin/metadata-key])
        metadata-value (rf/subscribe [:editor-plugin/metadata-value])]
    (fn []
      [:> Box {:class "space-y-radix-4 px-4 py-3"}
       [:> Flex {:justify "between" :align "center"}
        [:> Text {:as "label"
                  :size "1"
                  :class "text-gray-12"
                  :htmlFor "keep-metadata-switch"}
         "Keep Metadata after running"]
        [:> Switch {:id "keep-metadata-switch"
                    :checked @keep-metadata?
                    :size "1"
                    :aria-label "Keep metadata after running script"
                    :onCheckedChange #(rf/dispatch [:editor-plugin/toggle-keep-metadata])}]]
       [:div {:class "grid grid-cols-2 gap-small"}
        (doall
         (for [index (range (count @metadata))]
           ^{:key (str (:val (get @metadata index)) "-" index)}
           [:<>
            [forms/input {:on-change #(rf/dispatch [:editor-plugin/update-metadata-at-index
                                                    index
                                                    :key
                                                    (-> % .-target .-value)])
                          :size "1"
                          :variant "surface"
                          :placeholder "Name"
                          :not-margin-bottom? true
                          :aria-label (str "Metadata entry " (inc index) " name")
                          :value (get-in @metadata [index :key])}]
            [forms/input {:on-change #(rf/dispatch [:editor-plugin/update-metadata-at-index
                                                    index
                                                    :value
                                                    (-> % .-target .-value)])
                          :size "1"
                          :variant "surface"
                          :placeholder "Value"
                          :not-margin-bottom? true
                          :aria-label (str "Metadata entry " (inc index) " value")
                          :value (get-in @metadata [index :value])}]]))

        [forms/input {:on-change #(rf/dispatch [:editor-plugin/update-metadata-key
                                                (-> % .-target .-value)])
                      :size "1"
                      :variant "surface"
                      :placeholder "Name"
                      :not-margin-bottom? true
                      :aria-label "New metadata name"
                      :value @metadata-key}]
        [forms/input {:on-change #(rf/dispatch [:editor-plugin/update-metadata-value
                                                (-> % .-target .-value)])
                      :size "1"
                      :variant "surface"
                      :placeholder "Value"
                      :not-margin-bottom? true
                      :aria-label "New metadata value"
                      :value @metadata-value}]]

       [:div {:class "mt-4 flex justify-center w-full"}
        [:> Button {:onClick (fn []
                               (when (and @metadata-key @metadata-value)
                                 (rf/dispatch [:editor-plugin/add-metadata
                                               {:key @metadata-key
                                                :value @metadata-value}])))
                    :variant "soft"
                    :size "1"
                    :color "gray"
                    :aria-label "Add metadata entry"}
         "Add Metadata"]]])))
