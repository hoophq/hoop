(ns webapp.features.runbooks.setup.views.runbook-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.resource-role-filter :as resource-role-filter]
   [webapp.components.filtered-empty-state :refer [filtered-empty-state]]))

(defn main []
  (let [runbooks-rules-list (rf/subscribe [:runbooks-rules/list])
        selected-connection (r/atom nil)]
    (fn []
      (let [all-rules (or (:data @runbooks-rules-list) [])
            filtered-rules (if (nil? @selected-connection)
                             all-rules
                             (filter #(some #{@selected-connection} (:connection_names %))
                                     all-rules))]
        [:<>
         [:> Box {:mb "6"}
          [resource-role-filter/main {:selected @selected-connection
                                      :on-select #(reset! selected-connection %)
                                      :on-clear #(reset! selected-connection nil)
                                      :label "Resource Role"}]]
         [:> Box {:class "w-full h-full"}
          [:> Box {:class "min-h-full h-max"}
           (if (empty? filtered-rules)
             [filtered-empty-state {:entity-name "runbook rule"
                                    :filter-value @selected-connection}]
             (doall
              (for [rule filtered-rules]
                ^{:key (:id rule)}
                [:> Box {:class (str "first:rounded-t-lg border-x border-t "
                                     "last:rounded-b-lg bg-white last:border-b border-gray-200 "
                                     "p-[--space-5]")}
                 [:> Flex {:justify "between" :align "center"}
                  [:> Box
                   [:> Text {:size "4" :weight "bold"} (or (:name rule) (:id rule) "Unnamed Rule")]
                   [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
                    (or (:description rule) (str rule))]]
                  [:> Button {:variant "soft"
                              :color "gray"
                              :size "3"
                              :on-click #(rf/dispatch [:navigate :edit-runbooks-rule {} :rule-id (:id rule)])}
                   "Configure"]]])))]]]))))
