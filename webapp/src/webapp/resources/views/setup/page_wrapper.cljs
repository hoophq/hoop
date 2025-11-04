(ns webapp.resources.views.setup.page-wrapper
  (:require
   ["@radix-ui/themes" :refer [Avatar Button Box Badge Flex Text]]
   ["lucide-react" :refer [PackagePlus Check BrainCog UserRoundCheck]]
   [re-frame.core :as rf]
   [reagent.core :as r]))

(defn stepper-item [{:keys [title description completed? active? icon]}]
  [:> Flex {:gap "3"
            :align "center"
            :justify "between"
            :class "w-full p-radix-3 bg-white rounded-5 border border-gray-4"}
   [:> Flex {:gap "3" :align "center"}
    (if active?
      [:> Avatar {:size "4"
                  :variant "soft"
                  :color "green"
                  :fallback (r/as-element [icon "green"])}]

      [:> Avatar {:size "4"
                  :variant "soft"
                  :color "gray"
                  :fallback (r/as-element [icon "gray"])}])
    [:> Box
     [:> Text {:as "p"
               :size "3"
               :weight (if active? "medium" "regular")
               :class (cond
                        active? "text-gray-12"
                        completed? "text-gray-11"
                        :else "text-gray-11")}
      title]
     (when (and (not completed?)
                description)
       [:> Text {:as "p"
                 :size "2"
                 :class (cond
                          active? "text-gray-11"
                          completed? "text-gray-10"
                          :else "text-gray-10")}
        description])]]

   (when completed?
     [:> Box
      [:> Avatar {:size "1"
                  :radius "full"
                  :variant "soft"
                  :color "green"
                  :fallback (r/as-element
                             [:> Check {:size 16 :color "green"}])}]])])

(defn stepper-connector [{:keys [show-next?]}]
  (if show-next?
    [:> Box {:pl "6"}
     [:> Box {:class "w-[2px] h-6 bg-gray-6"}]
     [:> Badge {:size "1" :variant "soft" :color "gray" :radius "full" :class "-ml-[18px]"}
      "Next"]
     [:> Box {:class "w-[2px] h-6 bg-gray-6"}]]

    [:> Box {:pl "6"}
     [:> Box {:class "w-[2px] h-6 bg-gray-6"}]]))

(defn stepper []
  (let [current-step @(rf/subscribe [:resource-setup/current-step])]
    [:> Flex {:direction "column" :class "w-[350px] pt-24 px-10 pb-10 bg-gray-1"}
     [stepper-item {:title "Resource type"
                    :description "Quickly add your own services and databases or try a demo setup."
                    :icon (fn [color]
                            [:> PackagePlus {:size 18 :color color}])
                    :completed? (or (= current-step :agent-selector)
                                    (= current-step :roles))
                    :active? (= current-step :resource-name)}]

     [stepper-connector {:show-next? (= current-step :resource-name)}]

     [stepper-item {:title "Setup Agents"
                    :description "Establish secure communication with your infrastructure."
                    :icon (fn [color]
                            [:> BrainCog {:size 18 :color color}])
                    :completed? (= current-step :roles)
                    :active? (= current-step :agent-selector)}]

     [stepper-connector {:show-next? (= current-step :agent-selector)}]

     [stepper-item {:title "Resource roles"
                    :description "Configure permissions and usage details for your resource."
                    :icon (fn [color]
                            [:> UserRoundCheck {:size 18 :color color}])
                    :completed? false
                    :active? (= current-step :roles)}]]))

(defn main [{:keys [children footer-props onboarding?]}]
  (let [current-step @(rf/subscribe [:resource-setup/current-step])
        show-stepper? (and onboarding? (not= current-step :success))]
    (if onboarding?
      ;; Onboarding layout: with stepper on left
      [:> Box {:class "bg-gray-1 flex flex-col" :style {:height "calc(100vh - 72px)"}}
       ;; Main content area with stepper on left and children on right
       [:> Flex {:class "flex-1 min-h-0 overflow-hidden"}
        ;; Stepper on the left (only if not success step)
        (when show-stepper?
          [stepper])

        ;; Children content on the right
        [:> Box {:class (if show-stepper?
                          "flex-1 overflow-y-auto bg-gray-1"
                          "w-full overflow-y-auto bg-gray-1")}
         children]]

       ;; Footer with navigation buttons - fixed at bottom
       (when-not (:hide-footer? footer-props)
         [:> Flex {:justify "end"
                   :align "center"
                   :class "border-t border-gray-6 p-6 bg-white flex-shrink-0"}

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
                         :on-click #(if (= current-step :resource-name)
                                      (js/history.back)
                                      (rf/dispatch [:resource-setup->back]))}
              "Back"])

           (when-not (:next-hidden? footer-props)
             [:> Button {:size "2"
                         :disabled (:next-disabled? footer-props)
                         :on-click (:on-next footer-props)}
              (or (:next-text footer-props) "Next")])]])])))
