(ns webapp.components.notification-badge
  (:require
   ["@radix-ui/themes" :refer [Box IconButton]]))

(defn notification-badge
  "Icon button with a dot badge when has-notification? is true.
   badge-color is a background utility class and defaults to red.
   radius is passed through to the underlying IconButton; omit it to inherit
   the theme default."
  [{:keys [icon on-click active? has-notification? disabled? aria-label aria-expanded badge-color radius]}]
  [:> Box {:class "relative"}
   [:> IconButton
    (merge
     {:class (str (when active? "bg-gray-8 text-gray-12 ")
                  (when disabled? "cursor-not-allowed "))
      :size "2"
      :color "gray"
      :variant "soft"
      :highContrast true
      :disabled disabled?
      :on-click on-click}
     (when radius
       {:radius radius})
     (when aria-label
       {:aria-label aria-label})
     (when (some? aria-expanded)
       {:aria-expanded aria-expanded}))
    icon]
   (when has-notification?
     [:> Box {:class (str "absolute -top-1 -right-1 w-2 h-2 rounded-full "
                          (or badge-color "bg-red-500"))}])])
