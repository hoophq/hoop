(ns webapp.features.activation-journey.views.features-modal
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading IconButton Text]]
   ["lucide-react" :refer [X]]
   [re-frame.core :as rf]
   [webapp.features.activation-journey.views.enterprise-banner :as enterprise-banner]
   [webapp.features.activation-journey.views.feature-cards :as feature-cards]
   [webapp.features.promotion :as promotion]))

(defn main
  "\"See Features\" modal content: the three feature cards with the same
  locked/unlocked logic as the success page, plus (free plan only) the
  full-width enterprise upsell banner at the bottom."
  [{:keys [subtype connection-ids connection-names]}]
  (let [free? @(rf/subscribe [:activation-journey/free-license?])]
    [:> Box
     [:> Flex {:justify "between" :align "start" :gap "4" :class "mb-5"}
      [:> Box
       [:> Heading {:as "h2" :size "6" :weight "bold" :class "text-[--gray-12]"}
        "Protect your resource with our features"]
       [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
        "Three controls you can add to this resource"]]
      [:> IconButton {:size "2"
                      :variant "ghost"
                      :color "gray"
                      :type "button"
                      :aria-label "Close"
                      :on-click #(rf/dispatch [:modal->close])}
       [:> X {:size 16 :aria-hidden true}]]]
     [feature-cards/main {:subtype subtype
                          :surface :see-features-modal
                          :connection-ids connection-ids
                          :connection-names connection-names
                          :on-navigate #(rf/dispatch [:modal->close])}]
     (when free?
       [:> Box {:class "mt-5"}
        [enterprise-banner/main
         {:primary {:label "Talk to Sales"
                    :on-click promotion/request-demo}}]])]))

(defn open!
  "Opens the See Features modal via the global Radix modal system (default
  916px max width fits the three cards side by side, matching the handoff)."
  [props]
  (rf/dispatch [:modal->open
                {:id "activation-journey-features"
                 :content [main props]}]))
