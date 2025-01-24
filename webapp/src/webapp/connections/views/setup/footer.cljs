(ns webapp.connections.views.setup.footer
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex]]
   [re-frame.core :as rf]))

(defn main [{:keys [hide-footer?
                    on-back
                    back-text
                    on-next
                    next-text
                    next-disabled?
                    next-hidden?]}]
  (when-not hide-footer?
    [:> Flex {:justify "center" :class "fixed bottom-0 left-0 right-0 bg-white border-t border-[--gray-a6] px-7 py-4"}
     [:> Flex {:justify "between" :align "center" :class "w-[600px] px-6"}
      [:> Button {:size "2"
                  :variant "soft"
                  :color "gray"
                  :on-click (or on-back #(rf/dispatch [:connection-setup/go-back]))}
       (or back-text "Back")]

      (when-not next-hidden?
        [:> Button {:size "2"
                    :disabled next-disabled?
                    :on-click (or on-next #(rf/dispatch [:connection-setup/next-step]))}
         (or next-text "Next Configuration")])]]))
