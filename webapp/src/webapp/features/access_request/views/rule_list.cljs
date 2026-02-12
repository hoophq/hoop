(ns webapp.features.access-request.views.rule-list
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading IconButton Text]]
   ["lucide-react" :refer [ChevronRight]]
   [re-frame.core :as rf]))

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
  (let [rules (rf/subscribe [:access-request/rules])]
    (fn []
      (let [all-rules (or @rules [])
            processed-rules (sort-by :name all-rules)]

        [:> Box
         (if (empty? processed-rules)
           [:> Flex {:direction "column" :justify "center" :align "center" :class "h-40"}
            [:> Text {:size "3" :class "text-gray-500 text-center"}
             "No rules found"]]

           (doall
            (for [rule processed-rules]
              ^{:key (:name rule)}
              [rule-item rule])))]))))
