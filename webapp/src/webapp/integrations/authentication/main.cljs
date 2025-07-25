(ns webapp.integrations.authentication.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Separator Tabs Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.integrations.authentication.views.general-tab :as general-tab]
   [webapp.integrations.authentication.views.advanced-tab :as advanced-tab]
   [webapp.config :as config]))

(defn main []
  (let [auth-config (rf/subscribe [:authentication->config])
        active-tab (r/atom "general")
        min-loading-done (r/atom false)]

    (rf/dispatch [:authentication->get-config])

    ;; Set timer for minimum loading time
    (js/setTimeout #(reset! min-loading-done true) 1500)

    (fn []
      (let [loading? (or (= :loading (:status @auth-config))
                         (not @min-loading-done))
            submitting? (:submitting? @auth-config)]

        (cond
          loading?
          [:> Box {:class "bg-gray-1 h-full"}
           [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
            [loaders/simple-loader]]]

          :else
          [:> Box {:class "min-h-screen bg-gray-1"}
           ;; Header with Save button
           [:> Box {:class "sticky top-0 z-50 bg-gray-1 p-radix-7"}
            [:> Flex {:justify "between" :align "center"}
             [:> Heading {:as "h2" :size "8"} "Authentication"]
             [:> Button {:size "3"
                         :loading submitting?
                         :disabled submitting?
                         :on-click (fn []
                                     (rf/dispatch [:dialog->open
                                                   {:title "Save Authentication Configuration?"
                                                    :type :warning
                                                    :text-action-button "Save Configuration"
                                                    :action-button? true
                                                    :text [:> Box {:class "space-y-radix-4"}
                                                           [:> Text {:as "p"}
                                                            "Changing authentication settings may prevent access to your organization if misconfigured."]
                                                           [:> Text {:as "p"}
                                                            [:> Text {:as "span"}
                                                             "If you lose access, refer to the "]
                                                            [:> Text {:as "span" :weight "medium" :class "text-blue-600 underline cursor-pointer"
                                                                      :on-click #(js/window.open "https://hoop.dev/docs/setup/configuration/idp/get-started#troubleshooting" "_blank")}
                                                             "troubleshooting documentation"]
                                                            [:> Text {:as "span"}
                                                             " for recovery procedures."]]
                                                           [:> Text {:as "p" :weight "medium"}
                                                            "Are you sure you want to save these changes?"]]
                                                    :on-success (fn []
                                                                  (rf/dispatch [:authentication->save-config])
                                                                  (rf/dispatch [:modal->close]))}]))}
              "Save"]]]

           ;; Tabs content
           [:> Box {:class "p-radix-7"}
            [:> Box {:class "rounded-lg"}
             [:> Tabs.Root {:value @active-tab
                            :onValueChange #(reset! active-tab %)}
              [:> Tabs.List {:aria-label "Authentication tabs"}
               [:> Tabs.Trigger {:value "general"} "General"]
               [:> Tabs.Trigger {:value "advanced"} "Advanced Configuration"]]

              [:> Separator {:size "4"}]

              [:> Tabs.Content {:value "general" :class "py-radix-7"}
               [general-tab/main]]

              [:> Tabs.Content {:value "advanced" :class "py-radix-7"}
               [advanced-tab/main]]]]]])))))
