(ns webapp.features.runbooks.runner.views.metadata-panel
  (:require
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   ["@radix-ui/themes" :refer [Box Button Flex Switch Text]]))

(defn main []
  (let [keep-metadata? (rf/subscribe [:runbooks/keep-metadata?])
        metadata (rf/subscribe [:runbooks/metadata])
        metadata-key (rf/subscribe [:runbooks/metadata-key])
        metadata-value (rf/subscribe [:runbooks/metadata-value])]
    (fn []
      [:> Box {:class "space-y-radix-4 px-4 py-3"}
       [:> Flex {:justify "between" :align "center"}
        [:> Text {:as "p" :size "1" :class "text-gray-12"}
         "Keep Metadata after running"]
        [:> Switch {:checked @keep-metadata?
                    :size "1"
                    :onCheckedChange #(rf/dispatch [:runbooks/toggle-keep-metadata])}]]
       [:div {:class "grid grid-cols-2 gap-small"}
        (doall
         (for [index (range (count @metadata))]
           ^{:key (str (:val (get @metadata index)) "-" index)}
           [:<>
            [forms/input {:on-change #(rf/dispatch [:runbooks/update-metadata-at-index
                                                    index
                                                    :key
                                                    (-> % .-target .-value)])
                          :size "1"
                          :variant "surface"
                          :placeholder "Name"
                          :not-margin-bottom? true
                          :value (get-in @metadata [index :key])}]
            [forms/input {:on-change #(rf/dispatch [:runbooks/update-metadata-at-index
                                                    index
                                                    :value
                                                    (-> % .-target .-value)])
                          :size "1"
                          :variant "surface"
                          :placeholder "Value"
                          :not-margin-bottom? true
                          :value (get-in @metadata [index :value])}]]))

        [forms/input {:on-change #(rf/dispatch [:runbooks/update-metadata-key
                                                (-> % .-target .-value)])
                      :size "1"
                      :variant "surface"
                      :placeholder "Name"
                      :not-margin-bottom? true
                      :value @metadata-key}]
        [forms/input {:on-change #(rf/dispatch [:runbooks/update-metadata-value
                                                (-> % .-target .-value)])
                      :size "1"
                      :variant "surface"
                      :placeholder "Value"
                      :not-margin-bottom? true
                      :value @metadata-value}]]

       [:div {:class "mt-4 flex justify-center w-full"}
        [:> Button {:onClick (fn []
                               (when (and @metadata-key @metadata-value)
                                 (rf/dispatch [:runbooks/add-metadata
                                               {:key @metadata-key
                                                :value @metadata-value}])))
                    :variant "soft"
                    :size "1"
                    :color "gray"}
         "Add Metadata"]]])))

