(ns webapp.guardrails.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.features.promotion :as promotion]))

(defn panel []
  (let [guardrails-rules-list (rf/subscribe [:guardrails->list])
        min-loading-done (r/atom false)]
    (rf/dispatch [:guardrails->get-all])

    ;; Set timer for minimum loading time
    (js/setTimeout #(reset! min-loading-done true) 1500)

    (fn []
      (let [loading? (or (= :loading (:status @guardrails-rules-list))
                         (not @min-loading-done))]
        (cond
          loading?
          [:> Flex {:height "100%" :direction "column" :gap "5"
                    :class "bg-gray-1" :align "center" :justify "center"}
           [loaders/simple-loader]]

          (empty? (:data @guardrails-rules-list))
          [:> Box {:class "bg-gray-1 h-full"}
           [promotion/guardrails-promotion {:mode :empty-state}]]

          :else
          [:> Box {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
           [:header {:class "mb-7"}
            [:> Flex {:justify "between" :align "center"}
             [:> Box
              [:> Heading {:size "8" :weight "bold" :as "h1"}
               "Guardrails"]
              [:> Text {:size "5" :class "text-[--gray-11]"}
               "Create custom rules to guide and protect usage within your connections"]]

             (when (seq (:data @guardrails-rules-list))
               [:> Button {:size "3"
                           :variant "solid"
                           :on-click #(rf/dispatch [:navigate :create-guardrail])}
                "Create a new Guardrail"])]]

           [:> Box
            (for [rules (:data @guardrails-rules-list)]
              ^{:key (:id rules)}
              [:> Box {:class (str "first:rounded-t-lg border-x border-t "
                                   "last:rounded-b-lg bg-white last:border-b border-gray-200 "
                                   "p-[--space-5]")}
               [:> Flex {:justify "between" :align "center"}
                [:> Box
                 [:> Text {:size "4" :weight "bold"} (:name rules)]
                 [:> Text {:as "p" :size "3" :class "text-[--gray-11]"} (:description rules)]]
                [:> Button {:variant "soft"
                            :color "gray"
                            :size "3"
                            :on-click #(rf/dispatch [:navigate :edit-guardrail {} :guardrail-id (:id rules)])}
                 "Configure"]]])]])))))
