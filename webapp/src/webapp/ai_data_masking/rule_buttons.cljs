(ns webapp.ai-data-masking.rule-buttons
  (:require
   ["@radix-ui/themes" :refer [Button Flex]]
   ["lucide-react" :refer [Plus Trash2]]))

(defn main [{:keys [on-rule-add
                    on-toggle-select
                    select-state
                    selected?
                    on-toggle-all
                    on-rules-delete]}]
  [:> Flex {:align "center" :gap "2"}
   [:> Button {:size "2"
               :variant "soft"
               :on-click on-rule-add}
    [:> Plus {:size 14}]
    "New"]

   [:> Flex {:gap "2"}
    [:> Button {:size "2"
                :variant "soft"
                :color "gray"
                :on-click on-toggle-select}
     "Select"]

    (when @select-state
      [:<>
       [:> Button {:size "2"
                   :variant "soft"
                   :color "gray"
                   :on-click on-toggle-all}
        (if selected? "Unselect all" "Select all")]

       [:> Button {:size "2"
                   :variant "soft"
                   :color "red"
                   :on-click on-rules-delete}
        [:> Trash2 {:size 14}]
        "Delete"]])]])
