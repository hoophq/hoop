(ns webapp.features.access-request.views.free-license-callout
  (:require
   ["@radix-ui/themes" :refer [Callout Link]]
   ["lucide-react" :refer [Star]]
   [re-frame.core :as rf]))

(defn free-license-callout []
  [:> Callout.Root {:size "2" :color "accent" :class "mb-4" :highContrast true}
   [:> Callout.Icon
    [:> Star {:size 16 :style {:color "var(--accent-10)"}}]]
   [:> Callout.Text
    "Enable creating unlimited rules and applying to multiple connections for Command type requests by "
    [:> Link {:href "#"
              :class "font-medium"
              :on-click (fn [e]
                          (.preventDefault e)
                          (rf/dispatch [:navigate :upgrade-plan]))}
     "upgrading your plan."]]])
