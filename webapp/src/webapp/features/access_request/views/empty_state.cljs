(ns webapp.features.access-request.views.empty-state
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [re-frame.core :as rf]
   [webapp.config :as config]))

(defn main []
  [:> Box {:class "flex flex-col h-full items-center justify-between py-16 px-4 max-w-3xl mx-auto"}
   [:> Flex {:direction "column" :gap "3" :align "center"}
    [:> Box {:class "mb-8 w-80"}
     [:img {:src "/images/illustrations/empty-state.png"
            :alt "Empty state illustration"}]]

    [:> Flex {:direction "column" :align "center" :gap "3" :class "text-center"}
     [:> Text {:size "3" :class "text-gray-11 max-w-md text-center"}
      "No Access Request rules configured in your Organization yet"]]

    [:> Button {:size "3"
                :onClick #(rf/dispatch [:navigate :access-request-new])}
     "Create new Access Request rule"]

    [:> Flex {:align "center"}
     [:> Text {:class "text-gray-11 mr-1"}
      "Need more information? Check out"]
     [:a {:href (get-in config/docs-url [:features :reviews])
          :target "_blank"
          :class "text-blue-600 hover:underline"}
      "Access Request documentation"]
     [:> Text {:class "text-gray-11"}
      "."]]]])
