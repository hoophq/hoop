(ns webapp.features.ai-session-analyzer.views.rule-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.connection-filter :refer [connection-filter]]
   [webapp.components.filtered-empty-state :refer [filtered-empty-state]]))

(defn main []
  (let [rules-data (rf/subscribe [:ai-session-analyzer/rules])
        selected-connection (r/atom nil)]
    (fn []
      (let [rules (or (:data @rules-data) [])
            filtered-rules (if (nil? @selected-connection)
                             rules
                             (filter #(some #{@selected-connection} (:connection_names %))
                                     rules))]
        [:> Box {:class "w-full h-full space-y-radix-3"}
         [:> Flex {:pb "3"}
          [connection-filter {:selected @selected-connection
                              :on-select #(reset! selected-connection %)
                              :on-clear #(reset! selected-connection nil)}]]
         [:> Box {:class "min-h-full h-max"}
          (if (empty? filtered-rules)
            [filtered-empty-state {:entity-name "AI Session Analyzer rule"
                                   :filter-value @selected-connection}]
            (doall
             (for [rule rules]
               ^{:key (:id rule)}
               [:> Box {:class (str "first:rounded-t-lg border-x border-t "
                                    "last:rounded-b-lg bg-white last:border-b border-gray-200 "
                                    "p-[--space-5]")}
                [:> Flex {:justify "between" :align "start"}
                 [:> Box
                  [:> Text {:size "4" :weight "bold"} (or (:name rule) "Unnamed Rule")]
                  [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
                   (or (:description rule) "")]]
                 [:> Button {:variant "soft"
                             :color "gray"
                             :size "3"
                             :on-click #(rf/dispatch [:navigate :edit-ai-session-analyzer-rule {} :rule-name (:name rule)])}
                  "Configure"]]])))]]))))
