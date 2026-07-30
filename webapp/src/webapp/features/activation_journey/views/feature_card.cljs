(ns webapp.features.activation-journey.views.feature-card
  (:require
   ["@radix-ui/themes" :refer [Avatar Badge Box Button Card Flex Heading Text]]
   ["lucide-react" :refer [Check ShieldCheck VenetianMask]]
   [reagent.core :as r]))

;; The AI analyzer uses its own SVG glyph on a gray soft avatar (same look as
;; a locked avatar) in both the available and locked states; the other
;; features use a solid colored lucide avatar when available.
(def ^:private feature-icons
  {:guardrails {:icon ShieldCheck :color "indigo"}
   :masking {:icon VenetianMask :color "violet"}
   :ai-analyzer {:img-src "/icons/ai-analyser-icon.svg" :color "gray"}})

(defn main
  "One activation-journey feature card (success page and See Features modal).
  Takes a card model from the :activation-journey/cards sub and an on-cta
  handler invoked with the card when its action button is pressed. A card
  with :enabled? renders the done state (green check) instead of a setup CTA."
  [{:keys [feature title description badge locked? enabled?] :as card} on-cta]
  (let [{:keys [icon img-src color]} (get feature-icons feature)]
    [:> Card {:size "3" :variant "surface" :class "h-full"}
     [:> Flex {:direction "column" :gap "4" :class "h-full"}
      [:> Flex {:justify "between" :align "start" :gap "3"}
       (if enabled?
         [:> Avatar {:size "4"
                     :variant "soft"
                     :color "green"
                     :radius "large"
                     :fallback (r/as-element [:> Check {:size 20 :aria-hidden true}])}]
         [:> Avatar {:size "4"
                     :variant (if (or locked? img-src) "soft" "solid")
                     :color (if locked? "gray" color)
                     :radius "large"
                     :fallback (r/as-element
                                (if img-src
                                  [:img {:src img-src :alt "" :width 15 :height 15}]
                                  [:> icon {:size 20 :aria-hidden true}]))}])
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
