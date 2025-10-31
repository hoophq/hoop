(ns webapp.jira-templates.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Link Text]]
   ["lucide-react" :refer [Construction]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.features.promotion :as promotion]
   [webapp.jira-templates.template-list :as template-list]))

(defn panel []
  (let [jira-templates-list (rf/subscribe [:jira-templates->list])
        jira-integrations (rf/subscribe [:jira-integration->details])
        connections (rf/subscribe [:connections->pagination])
        user (rf/subscribe [:users->current-user])
        min-loading-done (r/atom false)]
    (rf/dispatch [:jira-templates->get-all])
    (rf/dispatch [:connections->get-connections])
    (rf/dispatch [:jira-integration->get])

    ;; Set timer for minimum loading time
    (js/setTimeout #(reset! min-loading-done true) 1500)

    (fn []
      (let [user-data (:data @user)
            free-license? (:free-license? user-data)
            loading? (or (:loading @connections)
                         (= :loading (:status @jira-templates-list))
                         (not @min-loading-done))]
        [:<>

         (cond
           loading?
           [:> Flex {:height "100%" :direction "column" :gap "5"
                     :class "bg-gray-1" :align "center" :justify "center"}
            [loaders/simple-loader]]

           (empty? (:data @jira-integrations))
           [:> Box {:class "bg-gray-1 h-full"}
            [promotion/jira-templates-promotion {:mode (if free-license?
                                                         :upgrade-plan
                                                         :empty-state)
                                                 :installed? false}]]

           (empty? (:data @jira-templates-list))
           [:> Box {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
            [:header {:class "mb-7"}
             [:> Flex {:justify "between" :align "center"}
              [:> Box
               [:> Heading {:size "8" :weight "bold" :as "h1"}
                "Jira Templates"]
               [:> Text {:size "5" :class "text-[--gray-11]"}
                "Optimize and automate workflows with Jira integration."]]]]

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
               "Jira templates documentation"] "."]]]

           :else
           [:> Box {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
            [:header {:class "mb-7"}
             [:> Flex {:justify "between" :align "center"}
              [:> Box
               [:> Heading {:size "8" :weight "bold" :as "h1"}
                "Jira Templates"]
               [:> Text {:size "5" :class "text-[--gray-11]"}
                "Optimize and automate workflows with Jira integration."]]

              [:> Button {:size "3"
                          :variant "solid"
                          :on-click #(rf/dispatch [:navigate :create-jira-template])}
               "Create new"]]]

            [template-list/main
             {:templates (:data @jira-templates-list)
              :on-configure #(rf/dispatch [:navigate :edit-jira-template {} :jira-template-id %])}]])]))))
