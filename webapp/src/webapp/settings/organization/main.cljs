(ns webapp.settings.organization.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text]]
   ["lucide-react" :refer [BarChart3 EyeOff ShieldOff]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.components.selection-card :refer [selection-card]]))

(def analytics-mode-options
  [{:value "identified"
    :icon BarChart3
    :title "Identified"
    :description "Share your data with our analytics tools so we can offer onboarding, support, and product updates."}
   {:value "anonymous"
    :icon EyeOff
    :title "Anonymous"
    :description "Send only hashed identifiers. No personally identifiable information leaves the gateway."}
   {:value "disabled"
    :icon ShieldOff
    :title "Disabled"
    :description "Stop all analytics events for this organization."}])

(defn main []
  (let [settings (rf/subscribe [:organization-settings])
        min-loading-done (r/atom false)]

    (rf/dispatch [:organization-settings->get-analytics-mode])
    (js/setTimeout #(reset! min-loading-done true) 800)

    (fn []
      (let [loading? (or (= :loading (:status @settings))
                         (not @min-loading-done))
            submitting? (:submitting? @settings)
            current-mode (or (:analytics-mode @settings) "identified")]

        (cond
          loading?
          [:> Box {:class "bg-gray-1 h-full"}
           [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
            [loaders/simple-loader]]]

          :else
          [:> Box {:class "min-h-screen bg-gray-1"}
           [:> Box {:class "sticky top-0 z-50 bg-gray-1 p-radix-7"}
            [:> Flex {:justify "between" :align "center"}
             [:> Heading {:as "h2" :size "8"} "Organization"]
             [:> Button {:size "3"
                         :loading submitting?
                         :disabled submitting?
                         :on-click #(rf/dispatch [:organization-settings->save-analytics-mode])}
              "Save"]]]

           [:> Box {:class "rounded-lg p-radix-7"}
            [:> Box {:class "space-y-radix-9"}

             [:> Grid {:columns "7" :gap "7"}
              [:> Box {:grid-column "span 2 / span 2"}
               [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
                "Analytics privacy"]
               [:> Text {:size "3" :class "text-[--gray-11]"}
                "Control how product usage data is shared with our analytics tools to help us improve Hoop. This setting applies to all members of your organization."]]

              [:> Box {:grid-column "span 5 / span 5"}
               [:> Box {:class "space-y-3"}
                (for [{:keys [value icon title description]} analytics-mode-options]
                  ^{:key value}
                  [selection-card
                   {:icon (r/as-element [:> icon {:size 20}])
                    :title title
                    :description description
                    :selected? (= current-mode value)
                    :on-click #(rf/dispatch [:organization-settings->set-analytics-mode value])}])]]]]]])))))
