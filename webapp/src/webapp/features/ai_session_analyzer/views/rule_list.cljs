(ns webapp.features.ai-session-analyzer.views.rule-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.resource-role-filter :refer [resource-role-filter]]))

(defn main []
  (let [rules-data (rf/subscribe [:ai-session-analyzer/rules])
        selected-connection (r/atom nil)
        on-select (fn [conn-name]
                    (reset! selected-connection conn-name)
                    (rf/dispatch [:ai-session-analyzer/get-rules {:connection-names [conn-name]}]))
        on-clear (fn []
                   (reset! selected-connection nil)
                   (rf/dispatch [:ai-session-analyzer/get-rules {}]))]
    (fn []
      (let [rules (or (:data @rules-data) [])]
        [:> Box {:class "w-full h-full space-y-radix-3"}
         [:> Flex {:pb "3"}
          [resource-role-filter {:selected @selected-connection
                                 :on-select on-select
                                 :on-clear on-clear}]]
         [:> Box {:class "min-h-full h-max"}
          (if (empty? rules)
            [:> Flex {:justify "center" :align "center" :class "h-40"}
             [:> Text {:size "3" :class "text-[--gray-11]"}
              (if @selected-connection
                "No rules found for this resource role"
                "No rules found")]]
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
