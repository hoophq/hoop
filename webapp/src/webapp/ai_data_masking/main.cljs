(ns webapp.ai-data-masking.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.features.promotion :as promotion]
   [webapp.ai-data-masking.rule-list :as rule-list]))

(defn main []
  (let [ai-data-masking-list (rf/subscribe [:ai-data-masking->list])
        min-loading-done (r/atom false)
        gateway-info (rf/subscribe [:gateway->info])]
    (rf/dispatch [:ai-data-masking->get-all])

    ;; Set timer for minimum loading time
    (js/setTimeout #(reset! min-loading-done true) 1500)

    (fn []
      (let [loading? (or (= :loading (:status @ai-data-masking-list))
                         (not @min-loading-done))
            redact-provider (-> @gateway-info :data :redact_provider)]
        (cond
          loading?
          [:> Flex {:height "100%" :direction "column" :gap "5"
                    :class "bg-gray-1" :align "center" :justify "center"}
           [loaders/simple-loader]]

          (empty? (:data @ai-data-masking-list))
          [:> Box {:class "bg-gray-1 h-full"}
           [promotion/ai-data-masking-promotion {:mode :empty-state
                                                 :redact-provider redact-provider}]]

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

           [rule-list/main
            {:rules (:data @ai-data-masking-list)
             :on-configure #(rf/dispatch [:navigate :edit-ai-data-masking {} :ai-data-masking-id %])}]])))))
