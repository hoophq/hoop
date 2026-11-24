(ns webapp.components.callout-link
  (:require
   ["@radix-ui/themes" :refer [Button]]
   ["lucide-react" :refer [ArrowUpRight]]))

;; A link out to the docs, drawn as a small secondary pill button with an
;; arrow, like DocsBtnCallOut in webapp_v2 (hoop-ui skill).
(defn main [{:keys [href text]}]
  [:> Button {:as-child true
              :size "2"
              :mt "4"
              :variant "outline"
              :color "gray"
              :radius "full"
              :highContrast true}
   [:a {:href href
        :target "_blank"
        :rel "noopener noreferrer"}
    [:> ArrowUpRight {:size 16 :aria-hidden true}]
    text]])
