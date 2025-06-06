(ns webapp.ai-data-masking.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   ["lucide-react" :refer [Construction]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.features.promotion :as promotion]))

(defn main []
  (let [ai-data-masking-list (rf/subscribe [:ai-data-masking->list])
        min-loading-done (r/atom false)]
    (rf/dispatch [:ai-data-masking->get-all])

    ;; Set timer for minimum loading time
    (js/setTimeout #(reset! min-loading-done true) 1500)

    (fn []
      (let [loading? (or (= :loading (:status @ai-data-masking-list))
                         (not @min-loading-done))]
        (cond
          loading?
          [:> Flex {:height "100%" :direction "column" :gap "5"
                    :class "bg-gray-1" :align "center" :justify "center"}
           [loaders/simple-loader]]

          (empty? (:data @ai-data-masking-list))
          [:> Box {:class "bg-gray-1 h-full"}
           [promotion/ai-data-masking-promotion {:mode :empty-state}]]

          :else
          [:> Box {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
           [:header {:class "mb-7"}
            [:> Flex {:justify "between" :align "center"}
             [:> Box
              [:> Heading {:size "8" :weight "bold" :as "h1"}
               "AI Data Masking"]
              [:> Text {:size "5" :class "text-[--gray-11]"}
               "Automatically mask sensitive data in real-time at the protocol layer"]]

             [:> Button {:size "3"
                         :variant "solid"
                         :on-click #(rf/dispatch [:navigate :create-ai-data-masking])}
              "Create new"]]]

           [:> Box
            (for [rule (:data @ai-data-masking-list)]
              ^{:key (:id rule)}
              [:> Box {:class (str "first:rounded-t-lg border-x border-t "
                                   "last:rounded-b-lg bg-white last:border-b border-gray-200 "
                                   "p-[--space-5]")}
               [:> Flex {:justify "between" :align "center"}
                [:> Box
                 [:> Text {:size "4" :weight "bold"} (:name rule)]
                 [:> Text {:as "p" :size "3" :class "text-[--gray-11]"} (:description rule)]]
                [:> Button {:variant "soft"
                            :color "gray"
                            :size "3"
                            :on-click #(rf/dispatch [:navigate :edit-ai-data-masking {} :ai-data-masking-id (:id rule)])}
                 "Configure"]]])]])))))
