(ns webapp.features.runbooks.setup.views.runbook-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Table Text]]
   [re-frame.core :as rf]))

(defn rule-item [{:keys [rule]}]
  (fn []
    [:> Table.Row {:align "center"}
     [:> Table.RowHeaderCell {:p "4"}
      [:> Text {:size "3" :weight "medium" :class "text-[--gray-12]"}
       (or (:name rule) (:id rule) "Unnamed Rule")]]
     [:> Table.Cell {:p "4"}
      [:> Text {:size "2" :class "text-[--gray-11]"}
       (str rule)]]
     [:> Table.Cell {:p "4"}
      [:> Flex {:gap "2"}
       [:> Button {:size "2"
                   :variant "soft"
                   :color "gray"
                   :on-click #(rf/dispatch [:navigate :edit-runbooks-rule {:rule-id (:id rule)}])}
        "Edit"]]]]))

(defn main []
  (let [runbooks-rules-list (rf/subscribe [:runbooks-rules/list])]

    (fn []
      (let [rules-list-state @runbooks-rules-list
            rules-data (or (:data rules-list-state) [])
            loading? (= (:status rules-list-state) :loading)
            error (:error rules-list-state)]
        [:> Box {:class "w-full h-full"}
         (cond
           loading?
           [:> Box {:class "flex items-center justify-center h-full"}
            [:> Text {:size "3" :class "text-[--gray-11]"}
             "Loading rules..."]]

           error
           [:> Box {:class "flex items-center justify-center h-full"}
            [:> Text {:size "3" :class "text-red-11"}
             (str "Error: " (or (:message error) "Failed to load rules"))]]

           (empty? rules-data)
           [:> Box {:class "flex items-center justify-center h-full"}
            [:> Text {:size "3" :class "text-[--gray-11]"}
             "No rules found"]]

           :else
           [:> Table.Root {:size "2" :variant "surface"}
            [:> Table.Header
             [:> Table.Row {:align "center"}
              [:> Table.ColumnHeaderCell "Name"]
              [:> Table.ColumnHeaderCell "Details"]
              [:> Table.ColumnHeaderCell "Actions"]]]
            [:> Table.Body
             (doall
              (for [rule rules-data]
                ^{:key (:id rule)}
                [rule-item {:rule rule}]))]])]))))
