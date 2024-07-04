(ns webapp.components.divider)

(defn main
  []
  [:div {:class "border-b mb-regular"}])

(defn labeled [label]
  [:div {:class "relative"}
   [:div {:class "absolute inset-0 flex items-center" :aria-hidden "true"}
    [:div {:class "w-full border-t border-gray-300"}]]
   [:div {:class "relative flex justify-center"}
    [:span {:class "bg-white px-2 text-sm text-gray-500"} label]]])
