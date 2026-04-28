(ns webapp.settings.api-keys.views.empty-state
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [re-frame.core :as rf]))

(defn main []
  [:> Box {:class "flex flex-col h-full items-center justify-between py-16 px-4 max-w-3xl mx-auto"}
   [:> Flex {:direction "column" :align "center"}
    [:> Box {:class "mb-8 w-80"}
     [:img {:src "/images/illustrations/empty-state.png"
            :alt "Empty state illustration"}]]

    [:> Flex {:direction "column" :align "center" :gap "3" :class "mb-8 text-center"}
     [:> Text {:size "3" :class "text-gray-11 max-w-md text-center"}
      "No API Keys configured in your Organization yet"]]

    [:> Button {:size "3"
                :on-click #(rf/dispatch [:navigate :settings-api-keys-new])}
     "Create a new API key"]]])
