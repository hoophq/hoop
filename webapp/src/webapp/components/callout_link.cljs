(ns webapp.components.callout-link
  (:require
   ["@radix-ui/themes" :refer [Callout Link]]
   ["lucide-react" :refer [ArrowUpRight]]))

(defn main [{:keys [href text]}]
  [:> Link {:href href
            :target "_blank"}
   [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
    [:> Callout.Icon
     [:> ArrowUpRight {:size 16}]]
    [:> Callout.Text
     text]]])
