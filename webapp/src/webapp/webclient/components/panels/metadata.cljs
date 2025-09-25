(ns webapp.webclient.components.panels.metadata
  (:require
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   ["@radix-ui/themes" :refer [Box Button Flex Switch Text]]))

(defn content []
  (let [keep-metadata? (rf/subscribe [:editor-plugin/keep-metadata?])
        metadata (rf/subscribe [:editor-plugin/metadata])
        metadata-key (rf/subscribe [:editor-plugin/metadata-key])
        metadata-value (rf/subscribe [:editor-plugin/metadata-value])]
    [:> Box {:class "space-y-radix-4"}
     [:> Flex {:justify "between" :align "center"}
      [:> Text {:as "p" :size "1" :class "text-gray-12"}
       "Keep Metadata after running"]
      [:> Switch {:checked @keep-metadata?
                  :size "1"
                  :onCheckedChange #(rf/dispatch [:editor-plugin/toggle-keep-metadata])}]]
     [:div {:class "grid grid-cols-2 gap-small"}
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
                       :value (get-in @metadata [index :key])}]
         [forms/input {:on-change #(rf/dispatch [:editor-plugin/update-metadata-at-index
                                                 index
                                                 :value
                                                 (-> % .-target .-value)])
                       :size "1"
                       :variant "surface"
                       :placeholder "Value"
                       :not-margin-bottom? true
                       :value (get-in @metadata [index :value])}]])

      [forms/input {:on-change #(rf/dispatch [:editor-plugin/update-metadata-key
                                              (-> % .-target .-value)])
                    :size "1"
                    :variant "surface"
                    :placeholder "Name"
                    :not-margin-bottom? true
                    :value @metadata-key}]
      [forms/input {:on-change #(rf/dispatch [:editor-plugin/update-metadata-value
                                              (-> % .-target .-value)])
                    :size "1"
                    :variant "surface"
                    :placeholder "Value"
                    :not-margin-bottom? true
                    :value @metadata-value}]]

     [:div {:class "mt-4 flex justify-center w-full"}
      [:> Button {:onClick (fn []
                             (when (and @metadata-key @metadata-value)
                               (rf/dispatch [:editor-plugin/add-metadata
                                             {:key @metadata-key
                                              :value @metadata-value}])))
                  :variant "soft"
                  :size "1"
                  :color "gray"}
       "Add Metadata"]]]))


(defn main [metadata]
  {:title "Metadata"
   :content [content metadata]})
