(ns webapp.components.dialog
  (:require ["react" :as react]
            ["@headlessui/react" :as ui]
            ["@heroicons/react/20/solid" :as hero-solid-icon]
            ["@radix-ui/themes" :refer [AlertDialog Flex Button]]
            [re-frame.core :as rf]
            [webapp.components.button :as button]
            [webapp.components.divider :as divider]))

(defmulti markup identity)
(defmethod markup :open [_ state]
  [:div.fixed.z-30.inset-0.overflow-y-auto
   {"aria-modal" true}
   [:div.fixed.w-full.h-full.inset-0.bg-gray-100.bg-opacity-90.transition
    {"aria-hidden" "true"
     :on-click #(rf/dispatch [:close-dialog])}]
   [:div.relative.max-w-lg.w-full
    {:class "bg-white border-2 rounded
               mx-auto top-1/3 p-regular"}
    [:span.block.font-bold.mb-regular.text-center
     (:text state)]
    (divider/main)
    [:footer.grid.grid-cols-2.gap-regular
     (button/secondary {:text "Cancel"
                        :full-width true
                        :on-click #(rf/dispatch [:close-dialog])})
     (button/primary {:text "Confirm"
                      :full-width true
                      :on-click (fn []
                                  ((:on-success state))
                                  (rf/dispatch [:close-dialog]))})]]])
(defmethod markup :default [_] nil)

(defn dialog
  []
  (let [dialog-state @(rf/subscribe [:dialog])]
    (markup (:status dialog-state) dialog-state)))

(defn new-dialog []
  (let [dialog-state (rf/subscribe [:new-dialog])]
    (fn []
      [:> AlertDialog.Root {:open (= (:status @dialog-state) :open)
                            :on-open-change #(rf/dispatch [:dialog->set-status %])}
       [:> AlertDialog.Content {:size "3"
                                :width "400px"
                                :max-width "600px"
                                :max-height "690px"}
        [:> AlertDialog.Title
         (:title @dialog-state)]
        [:> AlertDialog.Description
         (:text @dialog-state)]
        [:> Flex {:justify "end" :gap "3" :mt "4"}
         [:> AlertDialog.Cancel
          [:> Button {:color "gray"
                      :variant "soft"}
           "Cancel"]]
         [:> AlertDialog.Action
          [:> Button {:on-click #((:on-success @dialog-state))
                      :color "red"}
           (if (:text-action-button @dialog-state)
             (:text-action-button @dialog-state)
             "Delete")]]]]])))
