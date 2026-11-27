(ns webapp.features.machine-identities.views.empty-state
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [re-frame.core :as rf]))

;; Same shape as EmptyState in webapp_v2 (hoop-ui skill): illustration, a
;; "No <things> yet" title, one line on what to do, then the create button.
;; No docs link: Machine Identities has no docs page yet.
(defn main []
  [:> Flex {:direction "column" :align "center" :justify "center" :gap "6"
            :class "h-full py-16 px-4"}
   [:> Box {:class "w-80"}
    [:img {:src "/images/illustrations/empty-state.png"
           :alt ""}]]

   [:> Flex {:direction "column" :align "center" :gap "1" :class "text-center"}
    [:> Text {:as "p" :size "3" :weight "bold" :class "text-gray-11"}
     "No Machine Identities yet"]
    [:> Text {:as "p" :size "2" :class "text-gray-11"}
     "Create a Machine Identity to give a service its own access to Resources."]]

   [:> Button {:size "3"
               :radius "full"
               :onClick #(rf/dispatch [:navigate :machine-identities-new])}
    "Create Machine Identity"]])
