(ns webapp.parallel-mode.components.header-button
  (:require
   ["@radix-ui/themes" :refer [Badge Button Flex Tooltip]]
   ["lucide-react" :refer [FastForward]]
   [re-frame.core :as rf]))

(defn parallel-mode-button []
  (let [selected-count @(rf/subscribe [:parallel-mode/selected-count])
        is-active? @(rf/subscribe [:parallel-mode/is-active?])
        modal-open? @(rf/subscribe [:parallel-mode/modal-open?])]
    [:> Tooltip {:content "Parallel Mode"}
     [:> Button
      {:size "2"
       :variant (if is-active? "solid" "soft")
       :color (if is-active? "green" "gray")
       :class (str "min-w-[140px] "
                   (when modal-open? "ring-2 ring-green-500 ring-offset-2"))
       :onClick (fn []
                  (rf/dispatch [:parallel-mode/toggle-modal])
                  (when-not modal-open?
                    (rf/dispatch [:parallel-mode/seed-from-primary])))}
      [:> Flex {:align "center" :gap "2" :justify "center"}
       [:> FastForward {:size 16}]
       (if is-active?
         [:<>
          "Parallel Mode"
          [:> Badge {:variant "solid"
                     :color "gray"
                     :radius "full"
                     :size "1"
                     :class "ml-1 bg-white text-green-9"}
           selected-count]]
         "Parallel Mode")]]]))

