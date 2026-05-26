(ns webapp.shared-ui.free-license-banner
  (:require
   ["@radix-ui/themes" :refer [Callout Link]]
   ["lucide-react" :refer [AlertCircle Info]]
   [webapp.features.promotion :as promotion]))

(defn main
  "Free-license banner shown on gated feature pages.

   Props:
   - :variant      :info (default) for blue exploration callout,
                   :limit-reached for red wall.
   - :message      Body copy describing the limit for this feature.
   - :class        Optional extra classes for layout/spacing."
  [{:keys [variant message class]}]
  (let [limit? (= :limit-reached variant)
        color (if limit? "red" "blue")
        icon-component (if limit? AlertCircle Info)
        link-color (when limit? "red")]
    [:> Callout.Root (cond-> {:size "1" :color color}
                       (not limit?) (assoc :highContrast true)
                       class (assoc :class class))
     [:> Callout.Icon
      [:> icon-component {:size 16}]]
     [:> Callout.Text
      message " "
      [:> Link (cond-> {:href "#"
                        :class "font-medium"
                        :on-click (fn [e]
                                    (.preventDefault e)
                                    (promotion/request-demo))}
                 link-color (assoc :color link-color)
                 (not limit?) (assoc :style {:color "var(--blue-12)"}))
       "Contact our Sales team ↗"]]]))
