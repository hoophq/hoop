(ns webapp.webclient.components.features-indicator
  "Toolbar indicator that tells the user which hoop features are active on the
   selected resource role before they run anything.

   Hover lists the active features; clicking opens the Features Active modal.
   Renders nothing when no feature is active."
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Flex Heading HoverCard Text]]
   ["lucide-react" :refer [Radio]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.components.notification-badge :refer [notification-badge]]
   [webapp.webclient.features :as features]))

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
  (let [connection (rf/subscribe [:primary-connection/selected])
        analyzer-rule? (rf/subscribe [:ai-session-analyzer/role-has-rule?])]
    (fn []
      (let [active-features (features/active {:connection @connection
                                              :analyzer-rule? @analyzer-rule?})]
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

(defn main []
  (let [connection (rf/subscribe [:primary-connection/selected])
        analyzer-rule? (rf/subscribe [:ai-session-analyzer/role-has-rule?])]
    (fn []
      (let [active-features (features/active {:connection @connection
                                              :analyzer-rule? @analyzer-rule?})]
        (when (seq active-features)
          [:> HoverCard.Root
           ;; Trigger forces asChild, so it needs a plain DOM element to clone.
           [:> HoverCard.Trigger
            [:span {:class "inline-flex"}
             [notification-badge
              {:icon [:> Radio {:size 16}]
               :has-notification? true
               :badge-color "bg-[--green-9]"
               :aria-label (str "Features active on this resource role: "
                                (cs/join ", " (map :name active-features))
                                ". Open details")
               :on-click #(rf/dispatch [:modal->open
                                        {:id "features-active"
                                         :maxWidth "480px"
                                         :content [features-modal]}])}]]]

           [:> HoverCard.Content {:size "1" :maxWidth "300px"}
            [:> Flex {:direction "column" :gap "3"}
             (for [feature active-features]
               ^{:key (:id feature)} [hover-row feature])]]])))))
