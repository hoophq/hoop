(ns webapp.components.selection-card
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Flex Text]]))

;; A selectable option, drawn like SelectionCard in webapp_v2 (hoop-ui skill):
;; a square white card; selected is a light indigo tint with an indigo border,
;; never navy, so the page's primary button stays the strongest element.
;; Radix colour tokens are set inline: they need no Tailwind build step.
(defn selection-card [{:keys [icon title description selected? on-click badge icon-class disabled?]}]
  [:> Box {:p "3"
           :role "button"
           :tab-index (if disabled? -1 0)
           :aria-pressed (boolean selected?)
           :aria-disabled (boolean disabled?)
           :class (str "w-full " (if disabled? "cursor-not-allowed opacity-60" "cursor-pointer"))
           :style {:border-radius 0
                   :border (str "1px solid " (if selected? "var(--indigo-9)" "var(--gray-a6)"))
                   :background-color (if selected? "var(--indigo-3)" "white")}
           :on-click (when-not disabled? on-click)
           :on-key-down (when-not disabled?
                          (fn [e]
                            (when (contains? #{"Enter" " "} (.-key e))
                              (.preventDefault e)
                              (on-click))))}
   [:> Flex {:align "center" :gap "3"}
    (when icon
      [:> Avatar {:size "4"
                  :radius "none"
                  :class (or icon-class "")
                  :variant "soft"
                  :color (if selected? "indigo" "gray")
                  :fallback icon}])
    [:> Flex {:direction "column"}
     [:> Flex {:align "center" :gap "2"}
      [:> Text {:size "3" :weight "medium" :color "gray-12"} title]
      (when badge
        [:> Box {:class "text-xs font-medium px-2 py-0.5 rounded-full bg-success-9 text-white"}
         badge])]
     (when description
       [:> Text {:size "2" :class "text-[--gray-11]"} description])]]])
