(ns webapp.features.users.views.empty-state
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   [re-frame.core :as rf]))

(defn main []
  [:> Flex {:direction "column" :justify "center" :align "center" :height "100%" :gap "6"}
   [:> Box {:class "text-center"}
    [:img {:src "/images/illustrations/user-access-empty.png"
           :alt "No users illustration"
           :class "w-80 h-80 mx-auto mb-6"}]]

   [:> Box {:class "text-center space-y-4 max-w-md"}
    [:> Heading {:size "6" :weight "bold" :class "text-gray-12"}
     "All set."]
    [:> Text {:size "4" :class "text-gray-11"}
     "Go to your environment Identity Provider to manage users access."]]

   [:> Box {:class "text-center"}
    [:> Text {:size "3" :class "text-gray-11"}
     "Need more information? Check out "
     [:a {:href "#" :class "text-blue-600 hover:underline"}
      "Identity Providers documentation"]
     "."]]])
