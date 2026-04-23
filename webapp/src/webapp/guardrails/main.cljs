(ns webapp.guardrails.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.attribute-filter :as attribute-filter]
   [webapp.components.loaders :as loaders]
   [webapp.features.promotion :as promotion]
   [webapp.components.resource-role-filter :as resource-role-filter]
   [webapp.components.filtered-empty-state :refer [filtered-empty-state]]))

(defn panel []
  (let [guardrails-rules-list (rf/subscribe [:guardrails->list])
        min-loading-done (r/atom false)
        selected-connection (r/atom nil)
        selected-attribute (r/atom nil)
        connections (rf/subscribe [:connections])]
    (rf/dispatch [:guardrails->get-all])

    (js/setTimeout #(reset! min-loading-done true) 1500)

    (fn []
      (let [loading? (or (= :loading (:status @guardrails-rules-list))
                         (not @min-loading-done))
            all-rules (:data @guardrails-rules-list)
            connections-data @connections
            connections-results (:results connections-data)
            by-connection (if (nil? @selected-connection)
                            all-rules
                            (filter #(some #{@selected-connection}
                                           (map :name
                                                (filter (fn [conn]
                                                          (some #{(:id conn)} (:connection_ids %)))
                                                        (or connections-results []))))
                                    all-rules))
            filtered-rules (if (nil? @selected-attribute)
                             by-connection
                             (filter #(some #{@selected-attribute} (or (:attributes %) []))
                                     by-connection))]
        (cond
          loading?
          [:> Flex {:height "100%" :direction "column" :gap "5"
                    :class "bg-gray-1" :align "center" :justify "center"}
           [loaders/simple-loader]]

          (empty? all-rules)
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
               "Create custom rules to guide and protect usage within your resource roles"]]

             (when (seq all-rules)
               [:> Button {:size "3"
                           :variant "solid"
                           :on-click #(rf/dispatch [:navigate :create-guardrail])}
                "Create a new Guardrail"])]]

           [:> Flex {:mb "6" :gap "2"}
            [resource-role-filter/main {:selected @selected-connection
                                        :on-select #(reset! selected-connection %)
                                        :on-clear #(reset! selected-connection nil)
                                        :label "Resource Role"}]
            [attribute-filter/main {:selected @selected-attribute
                                    :on-select #(reset! selected-attribute %)
                                    :on-clear #(reset! selected-attribute nil)
                                    :label "Attribute"
                                    :placeholder "Search attributes"}]]

           [:> Box
            (if (empty? filtered-rules)
              [filtered-empty-state {:entity-name "guardrail"
                                     :filter-value (cond
                                                     (and @selected-connection @selected-attribute)
                                                     (str @selected-connection ", " @selected-attribute)

                                                     @selected-connection
                                                     @selected-connection

                                                     @selected-attribute
                                                     @selected-attribute)}]
              (for [rules filtered-rules]
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
                   "Configure"]]]))]])))))