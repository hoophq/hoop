(ns webapp.features.access-request.views.rule-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   [re-frame.core :as rf]))

(defn rule-item [{:keys [name description total-items]}]
  [:> Box {:class (str "first:rounded-t-6 last:rounded-b-6 "
                       "border-[--gray-a6] border "
                       (when (> total-items 1) "first:border-b-0"))}
   [:> Box {:p "5" :class "flex justify-between items-center"}
    [:> Flex {:direction "column" :gap "2"}
     [:> Heading {:as "h3" :size "5" :weight "medium" :class "text-[--gray-12]"}
      name]
     (when description
       [:> Text {:size "2" :class "text-[--gray-11]"}
        description])]

    [:> Button {:size "3"
                :variant "soft"
                :color "gray"
                :on-click #(rf/dispatch [:navigate :access-request-edit {} :rule-name name])}
     "Configure"]]])

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
              [rule-item (assoc rule :total-items (count processed-rules))])))]))))
