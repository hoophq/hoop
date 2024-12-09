(ns webapp.jira-templates.rule-buttons
  (:require
   ["@radix-ui/themes" :refer [Button Flex]]
   ["lucide-react" :refer [Plus]]))

(defn main [{:keys [on-rule-add on-toggle-select select-state selected? on-toggle-all on-rules-delete]}]
  [:> Flex {:gap "2"}
   [:> Button
    {:variant "soft"
     :size "2"
     :type "button"
     :on-click on-rule-add}
    [:> Plus {:size 16}]
    "New"]

   [:> Button
    {:variant "soft"
     :size "2"
     :type "button"
     :color "gray"
     :on-click on-toggle-select}
    (if @select-state "Cancel" "Select")]

   (when @select-state
     [:<>
      [:> Button
       {:variant "outline"
        :size "2"
        :type "button"
        :color "gray"
        :on-click on-toggle-all}
       (if selected? "Deselect all" "Select all")]

      [:> Button
       {:variant "outline"
        :size "2"
        :type "button"
        :color "red"
        :on-click on-rules-delete}
       "Delete"]])])
