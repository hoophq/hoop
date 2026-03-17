(ns webapp.features.ai-session-analyzer.views.empty-state
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [re-frame.core :as rf]
   [webapp.config :as config]))

(defn main [{:keys [provider-configured? on-configure]}]
  [:> Box {:class "flex flex-col h-full items-center justify-center py-16 px-4 max-w-3xl mx-auto"}
   [:> Flex {:direction "column" :align "center"}
    [:> Box {:class "mb-8 w-80"}
     [:img {:src "/images/illustrations/empty-state.png"
            :alt "Empty state illustration"}]]

    [:> Flex {:direction "column" :align "center" :gap "3" :class "mb-8 text-center"}
     [:> Text {:size "3" :class "text-gray-11 max-w-md text-center"}
      (if provider-configured?
        "No rules in your organization yet"
        "No configurations in your organization yet")]]

    (if provider-configured?
      [:> Button {:size "3"
                  :on-click #(rf/dispatch [:navigate :create-ai-session-analyzer-rule])}
       "Create new rule"]
      [:> Button {:size "3"
                  :on-click on-configure}
       "Configure AI Session Analyzer"])

    [:> Flex {:align "center" :class "text-sm mt-24"}
     [:> Text {:class "text-gray-11 mr-1"}
      "Need more information? Check out"]
     [:a {:href (get-in config/docs-url [:features :ai-session-analyzer])
          :target "_blank"
          :class "text-blue-600 hover:underline"}
      "AI Session Analyzer Configuration."]]]])
