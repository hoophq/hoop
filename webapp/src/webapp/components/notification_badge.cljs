(ns webapp.components.notification-badge
  (:require
   ["@radix-ui/themes" :refer [Box IconButton]]))

(defn notification-badge
  "Icon button with a dot badge when has-notification? is true.
   badge-color is a background utility class and defaults to red.
   radius is passed through to the underlying IconButton; omit it to inherit
   the theme default. It also decides where the dot sits, since a circular
   button's edge is not at the corner of its box.
   size and variant let a 40px toolbar use the small ghost scale without
   changing callers that want the defaults."
  [{:keys [icon on-click active? has-notification? disabled? aria-label aria-expanded
           badge-color radius size variant]
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
     (when radius
       {:radius radius})
     (when aria-label
       {:aria-label aria-label})
     (when (some? aria-expanded)
       {:aria-expanded aria-expanded}))
    icon]
   (when has-notification?
     ;; The dot straddles the button's edge, so where that edge is depends on the
     ;; shape. On a rounded square the corner of the box is the edge. On a circle
     ;; it is not: the 32px button inscribes a r=16 circle, so the box corner sits
     ;; 6.6px clear of it and a dot pinned there reads as detached. Pulling it to
     ;; the corner itself lands the dot 1px off the arc — half in, half out.
     [:> Box {:class (str "absolute w-2 h-2 rounded-full "
                          (if (= radius "full") "top-0 right-0 " "-top-1 -right-1 ")
                          (or badge-color "bg-red-500"))}])])
