(ns webapp.resources.views.setup.page-wrapper
  (:require
   ["@radix-ui/themes" :refer [Avatar Button Box Flex Text]]
   ["lucide-react" :refer [PackagePlus]]
   [re-frame.core :as rf]
   [reagent.core :as r]))

(defn stepper-item [{:keys [title description completed? active? icon]}]
  [:> Flex {:gap "3" :align "center" :class "w-full p-radix-3 bg-white rounded-lg border border-gray-4"}
   (if completed?
     [:> Avatar {:size "4"
                 :variant "soft"
                 :color "gray"
                 :fallback (r/as-element [icon "gray"])}]

     [:> Avatar {:size "4"
                 :variant "soft"
                 :color "green"
                 :fallback (r/as-element [icon "green"])}])
   [:> Box
    [:> Text {:as "p"
              :size "3"
              :weight (if active? "medium" "regular")
              :class (cond
                       active? "text-gray-12"
                       completed? "text-gray-11"
                       :else "text-gray-11")}
     title]
    (when description
      [:> Text {:as "p"
                :size "2"
                :class (cond
                         active? "text-gray-11"
                         completed? "text-gray-10"
                         :else "text-gray-10")}
       description])]])

(defn stepper []
  (let [current-step @(rf/subscribe [:resource-setup/current-step])]
    [:> Flex {:direction "column" :gap "3" :align "center" :justify "center" :class "w-[350px] p-10 bg-gray-1"}
     [stepper-item {:title "Resource type"
                    :icon (fn [color]
                            [:> PackagePlus {:size 18 :color color}])
                    :completed? true
                    :active? false}]

     [stepper-item {:title "Setup your Agents"
                    :description "Establish secure communication with your infrastructure."
                    :icon (fn [color]
                            [:> PackagePlus {:size 18 :color color}])
                    :completed? (contains? #{:roles :success} current-step)
                    :active? (= current-step :agent-selector)}]

     [stepper-item {:title "Resource roles"
                    :description "Configure permissions and usage details for your resource."
                    :icon (fn [color]
                            [:> PackagePlus {:size 18 :color color}])
                    :completed? (= current-step :success)
                    :active? (= current-step :roles)}]]))

(defn main [{:keys [children footer-props onboarding?]}]
  (if onboarding?
    ;; Onboarding layout: with stepper on left
    [:> Box {:class "h-screen bg-gray-1 flex flex-col"}
     ;; Main content area with stepper on left and children on right
     [:> Flex {:class "flex-1 min-h-0"}
      ;; Stepper on the left
      [stepper]

      ;; Children content on the right
      [:> Box {:class "flex-1 overflow-y-auto bg-gray-1"}
       children]]

     ;; Footer with navigation buttons - fixed at bottom
     (when-not (:hide-footer? footer-props)
       [:> Flex {:justify "end"
                 :align "center"
                 :class "border-t border-gray-6 p-6 bg-white"}

        [:> Flex {:gap "5" :align "center"}
         (when (:on-cancel footer-props)
           [:> Button {:size "2"
                       :variant "ghost"
                       :color "gray"
                       :on-click #(rf/dispatch [:resource-setup->back])}
            "Back"])

         (when-not (:next-hidden? footer-props)
           [:> Button {:size "2"
                       :disabled (:next-disabled? footer-props)
                       :on-click (:on-next footer-props)}
            (or (:next-text footer-props) "Next")])]])]

    ;; Normal layout: full-width without stepper
    [:> Box {:class "h-screen bg-gray-1 flex flex-col"}
     ;; Content fills full width
     [:> Box {:class "flex-1 overflow-y-auto bg-gray-1"}
      children]

     ;; Footer with navigation buttons - fixed at bottom
     (when-not (:hide-footer? footer-props)
       [:> Flex {:justify "end"
                 :align "center"
                 :class "border-t border-gray-6 p-6 bg-white"}

        [:> Flex {:gap "5" :align "center"}
         (when (:on-cancel footer-props)
           [:> Button {:size "2"
                       :variant "ghost"
                       :color "gray"
                       :on-click #(rf/dispatch [:resource-setup->back])}
            "Back"])

         (when-not (:next-hidden? footer-props)
           [:> Button {:size "2"
                       :disabled (:next-disabled? footer-props)
                       :on-click (:on-next footer-props)}
            (or (:next-text footer-props) "Next")])]])]))
