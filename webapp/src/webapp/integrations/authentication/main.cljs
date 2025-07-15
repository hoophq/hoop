(ns webapp.integrations.authentication.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Separator Tabs]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.integrations.authentication.views.general-tab :as general-tab]
   [webapp.integrations.authentication.views.advanced-tab :as advanced-tab]))

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
                         :on-click #(rf/dispatch [:authentication->save-config])}
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
