(ns webapp.parallel-mode.components.modal.footer
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   ["lucide-react" :refer [Info]]
   [re-frame.core :as rf]))

(defn main []
  (let [selected-count (rf/subscribe [:parallel-mode/selected-count])
        has-minimum? (rf/subscribe [:parallel-mode/has-minimum?])]
    (fn []
      [:> Box {:class (str "border-t border-gray-6 px-4 py-3 bg-gray-1 "
                           "absolute bottom-0 left-0 w-full z-10")}
       [:> Flex {:justify "between" :align "center" :gap "3"}
        (cond
          (<= @selected-count 1)
          [:> Text {:as "p" :size "2" :class "text-gray-11 flex items-center gap-2"}
           [:> Info {:size 16}]
           "Select at least two connections "]
          (>= @selected-count 2)
          [:> Button
           {:variant "ghost"
            :color "red"
            :size "2"
            :ml "2"
            :disabled (zero? @selected-count)
            :onClick #(rf/dispatch [:parallel-mode/clear-all])}
           "Unselect all"])

        ;; Right side - Warning + Actions
        [:> Flex {:align "center" :gap "3"}
         [:> Button
          {:variant "soft"
           :highContrast true
           :color "gray"
           :size "2"
           :onClick #(rf/dispatch [:parallel-mode/cancel-selection])}
          "Cancel"]

         [:> Button
          {:size "2"
           :disabled (not @has-minimum?)
           :onClick #(rf/dispatch [:parallel-mode/confirm-selection])}
          "Next"]]]])))
