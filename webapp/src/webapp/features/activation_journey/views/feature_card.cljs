(ns webapp.features.activation-journey.views.feature-card
  (:require
   ["@radix-ui/themes" :refer [Avatar Badge Box Button Card Flex Heading Text]]
   ["lucide-react" :refer [ShieldCheck Sparkles VenetianMask]]
   [reagent.core :as r]))

(def ^:private feature-icons
  {:guardrails {:icon ShieldCheck :color "indigo"}
   :masking {:icon VenetianMask :color "violet"}
   :ai-analyzer {:icon Sparkles :color "gray"}})

(defn main
  "One activation-journey feature card (success page and See Features modal).
  Takes a card model from the :activation-journey/cards sub and an on-cta
  handler invoked with the card when its action button is pressed."
  [{:keys [feature title description badge locked?] :as card} on-cta]
  (let [{:keys [icon color]} (get feature-icons feature)]
    [:> Card {:size "3" :variant "surface" :class "h-full"}
     [:> Flex {:direction "column" :gap "4" :class "h-full"}
      [:> Flex {:justify "between" :align "start" :gap "3"}
       [:> Avatar {:size "4"
                   :variant (if locked? "soft" "solid")
                   :color (if locked? "gray" color)
                   :radius "large"
                   :fallback (r/as-element [:> icon {:size 20 :aria-hidden true}])}]
       (when badge
         [:> Badge {:size "1" :variant "soft" :color (:color badge)}
          (:label badge)])]
      [:> Box {:class "flex-1"}
       [:> Heading {:as "h4" :size "3" :weight "bold"
                    :class (str "mb-1 " (if locked? "text-[--gray-11]" "text-[--gray-12]"))}
        title]
       [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
        description]]
      [:> Button {:size "2"
                  :variant "soft"
                  :type "button"
                  :class "w-full"
                  :on-click #(on-cta card)}
       (get-in card [:cta :label])]]]))
