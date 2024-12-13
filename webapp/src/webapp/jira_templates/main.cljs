(ns webapp.jira-templates.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text Link]]
   ["lucide-react" :refer [Construction]]
   [re-frame.core :as rf]
   [webapp.components.loaders :as loaders]
   [webapp.jira-templates.template-list :as template-list]))

(defn panel []
  (let [jira-templates-list (rf/subscribe [:jira-templates->list])
        jira-integrations (rf/subscribe [:jira-integration->details])
        connections (rf/subscribe [:connections])]
    (rf/dispatch [:jira-templates->get-all])
    (rf/dispatch [:connections->get-connections])
    (rf/dispatch [:jira-integration->get])
    (fn []
      [:> Box
       [:header {:class "mb-7"}
        [:> Flex {:justify "between" :align "center"}
         [:> Box
          [:> Heading {:size "8" :weight "bold" :as "h1"}
           "Jira Templates"]
          [:> Text {:size "5" :class "text-[--gray-11]"}
           "Optimize and automate workflows with Jira integration."]]

         (when (seq (:data @jira-templates-list))
           [:> Button {:size "3"
                       :variant "solid"
                       :on-click #(rf/dispatch [:navigate :create-jira-template])}
            "Create new"])]]

       (cond
         (or (:loading @connections)
             (= :loading (:status @jira-templates-list)))
         [:> Flex {:height "400px" :direction "column" :gap "5"
                   :class "p-[--space-5]" :align "center" :justify "center"}
          [loaders/simple-loader]]

         (empty? (:data @jira-integrations))
         [:> Flex {:height "400px" :direction "column" :gap "5"
                   :class "p-[--space-5]" :align "center" :justify "center"}
          [:> Construction {:size 48}]
          [:> Text {:size "3" :class "text-[--gray-11]"}
           "No Jira Integration configured in your Organization yet."]
          [:> Button {:size "3"
                      :variant "solid"
                      :on-click #(rf/dispatch [:navigate :jira-integration])}
           "Go to Jira Integration"]
          [:> Text {:size "2" :pt "5" :class "text-[--gray-11]"}
           "Need more information? Check out our "
           [:> Link {:href "#" :class "text-blue-500 hover:underline"}
            "Jira integrations documentation"] "."]]

         (empty? (:data @jira-templates-list))
         [:> Flex {:height "400px" :direction "column" :gap "5"
                   :class "p-[--space-5]" :align "center" :justify "center"}
          [:> Construction {:size 48}]
          [:> Text {:size "3" :class "text-[--gray-11]"}
           "No Jira template configured in your Organization yet."]
          [:> Button {:size "3"
                      :variant "solid"
                      :on-click #(rf/dispatch [:navigate :create-jira-template])}
           "Create a new JIRA Template"]
          [:> Text {:size "2" :pt "5" :class "text-[--gray-11]"}
           "Need more information? Check out our "
           [:> Link {:href "#" :class "text-blue-500 hover:underline"}
            "Jira templates documentation"] "."]]

         :else
         [template-list/main
          {:templates (:data @jira-templates-list)
           :on-configure #(rf/dispatch [:navigate :edit-jira-template {} :jira-template-id %])}])])))
