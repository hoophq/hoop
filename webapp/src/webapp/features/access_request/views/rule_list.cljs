(ns webapp.features.access-request.views.rule-list
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading IconButton Text]]
   ["lucide-react" :refer [ChevronRight]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.connection-filter :refer [connection-filter]]
   [webapp.components.filtered-empty-state :refer [filtered-empty-state]]))

(defn rule-item [{:keys [name description]}]
  [:> Box {:class (str "first:rounded-t-6 last:rounded-b-6 "
                       "border-[--gray-a6] border-x border-t last:border-b bg-white")}
   [:> Box {:p "5" :class "flex justify-between items-center min-h-[106px]"}
    [:> Flex {:direction "column" :gap "2"}
     [:> Heading {:as "h3" :size "5" :class "text-[--gray-12]"}
      name]
     (when description
       [:> Text {:size "3" :class "text-[--gray-11]"}
        description])]

    [:> IconButton {:size "3"
                    :variant "ghost"
                    :color "gray"
                    :on-click #(rf/dispatch [:navigate :access-request-edit {} :rule-name name])}
     [:> ChevronRight {:size 24}]]]])

(defn main []
  (let [rules (rf/subscribe [:access-request/rules])
        selected-connection (r/atom nil)]
    (fn []
      (let [all-rules (or @rules [])
            filtered-rules (if (nil? @selected-connection)
                             all-rules
                             (filter #(some #{@selected-connection} (:connection_names %))
                                     all-rules))
            processed-rules (sort-by :name filtered-rules)]

        [:<>
         [:> Box {:mb "6"}
          [connection-filter {:selected @selected-connection
                              :on-select #(reset! selected-connection %)
                              :on-clear #(reset! selected-connection nil)
                              :label "Resource Role"}]]

         [:> Box
          (if (empty? processed-rules)
            [filtered-empty-state {:entity-name "Access Request rule"
                                   :filter-value @selected-connection}]

            (doall
             (for [rule processed-rules]
               ^{:key (:name rule)}
               [rule-item rule])))]]))))
