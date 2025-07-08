(ns webapp.features.authentication.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Separator Tabs Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.features.authentication.views.general-tab :as general-tab]
   [webapp.features.authentication.views.advanced-tab :as advanced-tab]))

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
           [:> Box {:class "sticky top-0 z-50 bg-gray-1 px-7 py-5 border-b border-[--gray-a6]"}
            [:> Flex {:justify "between" :align "center"}
             [:> Heading {:as "h2" :size "8"} "Authentication"]
             [:> Button {:size "3"
                         :loading submitting?
                         :disabled submitting?
                         :on-click #(rf/dispatch [:authentication->save-config])}
              "Save"]]]

           ;; Tabs content
           [:> Box {:p "7"}
            [:> Box {:class "bg-white rounded-lg"}
             [:> Tabs.Root {:value @active-tab
                            :onValueChange #(reset! active-tab %)}
              [:> Tabs.List {:aria-label "Authentication tabs" :class "px-6 pt-6"}
               [:> Tabs.Trigger {:value "general"} "General"]
               [:> Tabs.Trigger {:value "advanced"} "Advanced Configuration"]]

              [:> Separator {:size "4"}]

              [:> Tabs.Content {:value "general" :class "p-6"}
               [general-tab/main]]

              [:> Tabs.Content {:value "advanced" :class "p-6"}
               [advanced-tab/main]]]]]])))))
