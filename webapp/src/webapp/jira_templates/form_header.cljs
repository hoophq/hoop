(ns webapp.jira-templates.form-header
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading]]
   [webapp.components.button :as button]
   [re-frame.core :as rf]))

(defn main [{:keys [form-type id scroll-pos]}]
  [:<>
   [:> Flex {:p "5" :gap "2"}
    [button/HeaderBack]]
   [:> Box {:class (str "sticky top-0 z-50 bg-gray-1 px-7 py-7 "
                        (when (>= @scroll-pos 30)
                          "border-b border-[--gray-a6]"))}
    [:> Flex {:justify "between"
              :align "center"}
     [:> Heading {:as "h2" :size "8"}
      (if (= :edit form-type)
        "Configure JIRA Template"
        "Create a new JIRA Template")]
     [:> Flex {:gap "5" :align "center"}
      (when (= :edit form-type)
        [:> Button {:size "4"
                    :variant "ghost"
                    :color "red"
                    :type "button"
                    :on-click #(rf/dispatch [:jira-templates->delete-by-id id])}
         "Delete"])
      [:> Button {:size "4"
                  :type "submit"}
       "Save"]]]]])
