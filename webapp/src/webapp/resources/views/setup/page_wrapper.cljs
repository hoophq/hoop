(ns webapp.resources.views.setup.page-wrapper
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text]]
   ["lucide-react" :refer [Check]]
   [re-frame.core :as rf]))

(defn stepper-item [{:keys [number label completed? active?]}]
  [:> Flex {:gap "3" :align "center" :class "py-3"}
   [:> Box {:class (str "flex items-center justify-center rounded-full w-8 h-8 "
                        (cond
                          completed? "bg-green-9"
                          active? "bg-blue-9"
                          :else "bg-gray-5"))}
    (if completed?
      [:> Check {:size 16 :class "text-white"}]
      [:> Text {:size "2" :weight "bold" :class "text-white"}
       number])]
   [:> Box
    [:> Text {:size "2"
              :weight (if active? "bold" "regular")
              :class (cond
                       active? "text-gray-12"
                       completed? "text-gray-11"
                       :else "text-gray-9")}
     label]]])

(defn stepper []
  (let [current-step @(rf/subscribe [:resource-setup/current-step])]
    [:> Box {:class "w-64 bg-gray-2 border-r border-gray-6 p-6 space-y-2"}
     [stepper-item {:number "1"
                    :label "Resource type"
                    :completed? true
                    :active? false}]

     [stepper-item {:number "2"
                    :label "Setup your Agents"
                    :completed? (contains? #{:roles :success} current-step)
                    :active? (= current-step :agent-selector)}]

     [stepper-item {:number "3"
                    :label "Resource roles"
                    :completed? (= current-step :success)
                    :active? (= current-step :roles)}]]))

(defn main [{:keys [children footer-props]}]
  [:> Flex {:class "h-screen overflow-hidden"}
   [stepper]

   [:> Flex {:direction "column" :class "flex-1 overflow-hidden"}
    ;; Content area
    [:> Box {:class "flex-1 overflow-y-auto"}
     children]

    ;; Footer with navigation buttons
    (when-not (:hide-footer? footer-props)
      [:> Flex {:justify "between"
                :align "center"
                :class "border-t border-gray-6 p-6 bg-white"}
       [:> Box
        (when-not (:back-hidden? footer-props)
          [:button {:class "text-sm text-gray-11 hover:text-gray-12"
                    :on-click #(rf/dispatch [:resource-setup->back])}
           "Back"])]

       [:> Flex {:gap "3"}
        (when (:on-cancel footer-props)
          [:button {:class "px-4 py-2 text-sm text-gray-11 hover:text-gray-12"
                    :on-click (:on-cancel footer-props)}
           "Cancel"])

        (when-not (:next-hidden? footer-props)
          [:button {:class (str "px-4 py-2 rounded-md text-sm font-medium "
                                (if (:next-disabled? footer-props)
                                  "bg-gray-5 text-gray-9 cursor-not-allowed"
                                  "bg-blue-9 text-white hover:bg-blue-10"))
                    :disabled (:next-disabled? footer-props)
                    :on-click (:on-next footer-props)}
           (or (:next-text footer-props) "Next")])]])]])
