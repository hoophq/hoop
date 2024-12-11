(ns webapp.jira-templates.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   ["lucide-react" :refer [Construction]]
   [re-frame.core :as rf]
   [webapp.jira-templates.template-list :as template-list]))

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

         [template-list/main
          {:templates (:data @jira-templates-rules-list)
           :on-configure #(rf/dispatch [:navigate :edit-jira-template {} :jira-template-id %])}])])))
