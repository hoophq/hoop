(ns webapp.jira-templates.main
  (:require [re-frame.core :as rf]
            ["lucide-react" :refer [Construction]]
            ["@radix-ui/themes" :refer [Box Button Flex Text Heading]]))

(defn panel []
  (let [jira-templates-rules-list (rf/subscribe [:jira-templates->list])]
    (rf/dispatch [:jira-templates->get-all])
    (fn []
      [:> Box
       [:header {:class "mb-7"}
        [:> Flex {:justify "between" :align "center"}
         [:> Box
          [:> Heading {:size "8" :weight "bold" :as "h1"}
           "JIRA Templates"]
          [:> Text {:size "5" :class "text-[--gray-11]"}
           "Create custom rules to guide and protect usage within your connections"]]

         (when (seq (:data @jira-templates-rules-list))
           [:> Button {:size "3"
                       :variant "solid"
                       :on-click #(rf/dispatch [:navigate :create-jira-template])}
            "Create a new JIRA template"])]]
       (if (empty? (:data @jira-templates-rules-list))
         [:> Flex {:height "400px" :direction "column" :gap "5" :class "p-[--space-5]" :align "center" :justify "center"}
          [:> Construction {:size 48}]
          [:> Text {:size "3" :class "text-[--gray-11]"}
           "No JIRA template configured in your Organization yet."]
          [:> Button {:size "3"
                      :variant "solid"
                      :on-click #(rf/dispatch [:navigate :create-jira-template])}
           "Create a new JIRA Template"]
          [:> Text {:size "2" :pt "5" :class "text-[--gray-11]"}
           "Need more information? Check out our JIRA templates documentation."]]

         [:> Box
          (for [rules (:data @jira-templates-rules-list)]
            ^{:key (:id rules)}
            [:> Box {:class (str "first:rounded-t-lg border-x border-t "
                                 "last:rounded-b-lg bg-white last:border-b border-gray-200 "
                                 "p-[--space-5]")}
             [:> Flex {:justify "between" :align "center"}
              [:> Box
               [:> Text {:size "4" :weight "bold"} (:name rules)]
               [:> Text {:as "p" :size "3" :class "text-[--gray-11]"} (:description rules)]]
              [:> Button {:variant "soft"
                          :color "gray"
                          :size "3"
                          :on-click #(rf/dispatch [:navigate :edit-jira-template {} :jira-template-id (:id rules)])}
               "Configure"]]])])])))
