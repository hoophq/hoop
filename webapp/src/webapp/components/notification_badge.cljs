(ns webapp.components.notification-badge
  (:require
   ["@radix-ui/themes" :refer [Box IconButton]]))

(defn notification-badge
  "Icon button with a red badge when has-notification? is true."
  [{:keys [icon on-click active? has-notification? disabled? aria-label aria-expanded
           size variant]
    :or {size "2" variant "soft"}}]
  ;; inline-flex so the wrapper shrink-wraps the button. The ghost variant has
  ;; no fixed box — Radix sizes it from padding and pulls it back with negative
  ;; margins — so a block wrapper kept its own larger box and the icon drifted
  ;; out of line with the sibling buttons. It also puts the notification dot on
  ;; the button's real corner rather than the wrapper's.
  [:> Box {:class "relative inline-flex"}
   [:> IconButton
    (merge
     {:class (str (when active? "bg-gray-8 text-gray-12 ")
                  (when disabled? "cursor-not-allowed "))
      :size size
      :color "gray"
      :variant variant
      :highContrast true
      :disabled disabled?
      :on-click on-click}
     (when aria-label
       {:aria-label aria-label})
     (when (some? aria-expanded)
       {:aria-expanded aria-expanded}))
    icon]
   (when has-notification?
     [:> Box {:class (str "absolute -top-1 -right-1 w-2 h-2 "
                          "rounded-full bg-red-500")}])])
