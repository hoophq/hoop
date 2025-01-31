(ns webapp.connections.views.setup.footer
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex]]
   [re-frame.core :as rf]))

(defn main [{:keys [hide-footer?
                    on-back
                    back-text
                    on-click
                    on-next
                    next-text
                    next-disabled?
                    next-hidden?]}]
  (let [sidebar-desktop (rf/subscribe [:sidebar-desktop])]
    (when-not hide-footer?
      [:> Flex {:justify "center"
                :class (str "fixed bottom-0 bg-white border-t border-[--gray-a6] px-7 py-4 "
                            (if (= :opened (:status @sidebar-desktop))
                              "left-side-menu-width right-0" ; When sidebar is open
                              "left-[72px] right-0"))}       ; When sidebar is closed
       [:> Flex {:justify "between"
                 :align "center"
                 :class "w-[600px] px-6"}
        [:> Button {:size "2"
                    :variant "soft"
                    :color "gray"
                    :on-click (or on-back #(rf/dispatch [:connection-setup/go-back]))}
         (or back-text "Back")]

        (when-not next-hidden?
          [:> Button {:size "2"
                      :disabled next-disabled?
                      :on-click (fn [e]
                                  (when on-click
                                    (on-click e))
                                  (when on-next
                                    (on-next e)))}
           (or next-text "Next Configuration")])]])))
