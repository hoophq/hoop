(ns webapp.webclient.components.features-indicator
  "Toolbar indicator that tells the user which hoop features are active on the
   selected resource role before they run anything.

   Hover lists the active features; clicking opens the Features Active modal.
   Renders nothing when no feature is active."
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Flex Heading HoverCard Text Tooltip]]
   ["lucide-react" :refer [Radio]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.notification-badge :refer [notification-badge]]
   [webapp.webclient.features :as features]))

(def ^:private hover-variant
  "Which hover surface to show. Two are implemented:

     :card    a light popover, one row per feature with an icon and an Active
              badge — what we ship
     :tooltip the Figma design: a plain dark tooltip, one `{Feature}: Active`
              line per feature, no icons

   The Figma specifies `:tooltip`. We ship `:card` because it reads better with
   the tiles the modal already uses, and kept the tooltip primed so switching
   back is one keyword if anyone objects. Delete the unused branch once that
   settles — this is not meant to live here forever."
  :card)

(defn- feature-tile [{:keys [icon tile-class]}]
  [:> Box {:class (str "flex items-center justify-center shrink-0 rounded-1 w-6 h-6 "
                       tile-class)}
   icon])

(defn- hover-row [{:keys [name] :as feature}]
  [:> Flex {:align "center" :justify "between" :gap "3"}
   [:> Flex {:align "center" :gap "2"}
    [feature-tile feature]
    [:> Text {:size "2" :weight "medium"} name]]
   [:> Badge {:variant "soft" :color "green"} "Active"]])

(defn- tooltip-lines
  "Figma variant: one `{Feature}: Active` line per feature. Uses the catalog's
   own names, not the ones drawn in Figma — that file still says \"AI Data
   Masking\", which RD-231 renamed to \"Live Data Masking\"."
  [active-features]
  (r/as-element
   [:> Flex {:direction "column" :align "center"}
    (for [{:keys [id name]} active-features]
      ^{:key id} [:span (str name ": Active")])]))

(defn- feature-card [{:keys [name description] :as feature}]
  [:> Flex {:align "start" :gap "3"
            :class "p-3 rounded-md border border-[--gray-4]"}
   [feature-tile feature]
   [:> Flex {:direction "column" :gap "1"}
    [:> Text {:size "2" :weight "medium"} name]
    [:> Text {:size "1" :class "text-[--gray-11]"} description]]])

(defn- features-modal
  "Modal body. Subscribes rather than receiving a snapshot so it keeps up with
   the selected resource role while it is open."
  []
  (let [connection (rf/subscribe [:primary-connection/selected])]
    (fn []
      (let [active-features (features/active @connection)]
        [:> Flex {:direction "column" :gap "5"}
         [:> Flex {:direction "column" :gap "1"}
          [:> Heading {:as "h2" :size "4" :weight "bold"} "Features Active"]
          [:> Text {:size "2" :class "text-[--gray-11]"}
           "The following features are active and will affect this session."]]

         (if (seq active-features)
           [:> Flex {:direction "column" :gap "2"}
            (for [feature active-features]
              ^{:key (:id feature)} [feature-card feature])]
           [:> Text {:size "2" :class "text-[--gray-11]"}
            "No features active for this session."])

         [:> Flex {:justify "end"}
          [:> Button {:variant "soft"
                      :color "gray"
                      :on-click #(rf/dispatch [:modal->close])}
           "Close"]]]))))

(defn- indicator-button
  "Call this with parens, not brackets. Both hover surfaces clone their trigger
   via `asChild`, which needs a real DOM element — rendering this as a Reagent
   component would put a function component in between, and those do not forward
   refs. Calling it inlines the `[:span]` as the trigger's direct child."
  [active-features]
  [:span {:class "inline-flex"}
   [notification-badge
    {:icon [:> Radio {:size 16}]
     :has-notification? true
     :badge-color "bg-[--green-9]"
     ;; Circular, unlike the square-ish toolbar icons next to it — the Figma
     ;; sets it apart from them on purpose.
     :radius "full"
     :aria-label (str "Features active on this resource role: "
                      (cs/join ", " (map :name active-features))
                      ". Open details")
     :on-click #(rf/dispatch [:modal->open
                              {:id "features-active"
                               :maxWidth "480px"
                               :content [features-modal]}])}]])

(defn main []
  (let [connection (rf/subscribe [:primary-connection/selected])]
    (fn []
      (let [active-features (features/active @connection)]
        (when (seq active-features)
          (if (= hover-variant :tooltip)
            [:> Tooltip {:content (tooltip-lines active-features)}
             (indicator-button active-features)]

            [:> HoverCard.Root
             [:> HoverCard.Trigger
              (indicator-button active-features)]
             [:> HoverCard.Content {:size "1" :maxWidth "300px"}
              [:> Flex {:direction "column" :gap "3"}
               (for [feature active-features]
                 ^{:key (:id feature)} [hover-row feature])]]]))))))
