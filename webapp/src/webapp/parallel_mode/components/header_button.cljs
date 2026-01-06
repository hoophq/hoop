(ns webapp.parallel-mode.components.header-button
  (:require
   ["@radix-ui/themes" :refer [Badge Button Flex IconButton Tooltip]]
   ["lucide-react" :refer [FastForward X]]
   [re-frame.core :as rf]))

(defn parallel-mode-button []
  (let [selected-count @(rf/subscribe [:parallel-mode/selected-count])
        is-active? @(rf/subscribe [:parallel-mode/is-active?])
        modal-open? @(rf/subscribe [:parallel-mode/modal-open?])]
    [:> Tooltip {:content "Parallel Mode"}
     [:> Flex {:align "center" :gap "1"}
      [:> Button
       {:size "2"
        :variant "soft"
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
                      :color "green"
                      :radius "full"
                      :size "1"}
            selected-count]
           [:> IconButton
            {:size "1"
             :variant "ghost"
             :color "green"
             :onClick (fn [e]
                        (.stopPropagation e)
                        (rf/dispatch [:parallel-mode/clear-all])
                        (rf/dispatch [:parallel-mode/close-modal]))}
            [:> X {:size 14}]]]

          "Parallel Mode")]]]]))

