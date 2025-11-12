(ns webapp.features.runbooks.setup.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Separator Tabs Text]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.headings :as h]
   [webapp.features.promotion :as promotion]
   [webapp.features.runbooks.setup.views.configuration-view :as config-view]
   [webapp.features.runbooks.setup.views.empty-state :as empty-state]
   [webapp.features.runbooks.setup.views.runbook-list :as runbook-list]))

(defn parse-params [params]
  (let [params (js/decodeURIComponent params)
        params (cs/split params "=")
        params (second params)]
    params))

(defn main []
  (let [plugin-details (rf/subscribe [:plugins->plugin-details])
        runbooks-rules-list (rf/subscribe [:runbooks-rules/list])
        active-tab (r/atom "rules")
        params (.-search (.-location js/window))
        url-tab (r/atom (parse-params params))]

    (rf/dispatch [:runbooks/list])
    (rf/dispatch [:runbooks-rules/get-all])

    (fn []
      (let [plugin (:plugin @plugin-details)
            installed? (or (:installed? plugin) false)
            has-rules? (seq (or (:data @runbooks-rules-list) []))]

        ;; Initialize active tab from URL
        (when @url-tab
          (reset! active-tab @url-tab)
          (reset! url-tab nil))

        (if (and
             (or (not installed?)
                 (empty? (:config plugin)))
             (not (boolean
                   (.getItem (.-localStorage js/window) "runbooks-promotion-seen"))))
          [:> Box {:class "bg-gray-1 h-full"}
           [promotion/runbooks-promotion {:mode :empty-state
                                          :installed? false}]]

          [:> Box {:class "flex flex-col bg-white px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
           [:> Flex {:direction "column" :gap "5" :class "h-full"}

            [:> Flex {:justify "between" :align "center"}
             [:> Box
              [h/h2 "Runbooks" {:class "text-[--gray-12]"}]
              [:> Text {:as "p" :size "3" :class "text-gray-500"}
               "Manage access paths per resource role and integrate Git repositories to automate runbook execution."]]
             (when has-rules?
               [:> Button {:size "3"
                           :variant "solid"
                           :on-click #(rf/dispatch [:navigate :create-runbooks-rule])}
                "Create Runbooks Rule"])]

            [:> Box {:class "flex-grow"}
             [:> Tabs.Root {:value @active-tab
                            :onValueChange #(reset! active-tab %)}
              [:> Tabs.List {:aria-label "Runbooks tabs"}
               [:> Tabs.Trigger {:value "rules"} "Runbooks Rules"]
               [:> Tabs.Trigger {:value "configurations"} "Configurations"]]

              [:> Separator {:size "4" :mb "7"}]

              [:> Tabs.Content {:value "rules" :class "h-full"}
               (if (not has-rules?)
                 [empty-state/main installed?]
                 [runbook-list/main])]

              [:> Tabs.Content {:value "configurations" :class "h-full"}
               [config-view/main active-tab]]]]]])))))
