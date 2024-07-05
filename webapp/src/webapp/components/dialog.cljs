(ns webapp.components.dialog
  (:require ["react" :as react]
            ["@headlessui/react" :as ui]
            ["@heroicons/react/20/solid" :as hero-solid-icon]
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

(def type-dictionary
  {:danger {:icon [:> hero-solid-icon/ExclamationTriangleIcon
                   {:class "h-6 w-6 text-red-600" :aria-hidden "true"}]
            :icon-bg "bg-red-100"
            :button-class "bg-red-600 hover:bg-red-500"}
   :info {:icon [:> hero-solid-icon/InformationCircleIcon
                 {:class "h-6 w-6 text-blue-600" :aria-hidden "true"}]
          :icon-bg "bg-blue-100"
          :button-class "bg-blue-600 hover:bg-blue-500"}})

(defn new-dialog []
  (let [dialog-state (rf/subscribe [:new-dialog])]
    (fn []
      [:> ui/Transition {:show (= (:status @dialog-state) :open)
                         :as react/Fragment}
       [:> ui/Dialog {:class "relative z-50"
                      :on-close #(rf/dispatch [:dialog->close])}
        [:> (.-Child ui/Transition) {:enter "ease-out duration-300"
                                     :enter-from "opacity-0"
                                     :enter-to "opacity-100"
                                     :leave "ease-in duration-200"
                                     :leave-from "opacity-100"
                                     :leave-to "opacity-0"}
         [:div {:class "fixed inset-0 bg-gray-500 bg-opacity-75 transition-opacity"}]]
        [:div {:class "fixed inset-0 z-10 w-screen overflow-y-auto"}
         [:div {:class "flex min-h-full items-end justify-center p-4 text-center sm:items-center sm:p-0"}
          [:> (.-Child ui/Transition) {:enter "ease-out duration-300"
                                       :enter-from "opacity-0 translate-y-4 sm:translate-y-0 sm:scale-95"
                                       :enter-to "opacity-100 translate-y-0 sm:scale-100"
                                       :leave "ease-in duration-200"
                                       :leave-from "opacity-100 translate-y-0 sm:scale-100"
                                       :leave-to "opacity-0 translate-y-4 sm:translate-y-0 sm:scale-95"}
           [:> (.-Panel ui/Dialog)
            {:class "relative transform overflow-hidden rounded-lg bg-white px-4 pb-4 pt-5 text-left shadow-xl transition-all sm:my-8 sm:w-full sm:max-w-lg sm:p-6"}
            [:div {:class "sm:flex sm:items-start"}
             [:div {:class (str "mx-auto flex h-12 w-12 flex-shrink-0 items-center "
                                "justify-center rounded-full sm:mx-0 sm:h-10 sm:w-10 "
                                (get-in type-dictionary [(:type @dialog-state) :icon-bg]))}
              (get-in type-dictionary [(:type @dialog-state) :icon])]
             [:div {:class "mt-3 text-center sm:ml-4 sm:mt-0 sm:text-left"}
              (when (:title @dialog-state)
                [:> (.-Title ui/Dialog) {:as "h3" :class "text-base font-semibold leading-6 text-gray-900"}
                 (:title @dialog-state)])
              [:div {:class "mt-2"}
               [:p {:class "text-sm text-gray-500"}
                (:text @dialog-state)]]]]
            [:div {:class "mt-5 sm:mt-4 sm:flex sm:flex-row-reverse"}
             [:button {:type "button"
                       :class (str "inline-flex w-full justify-center rounded-md px-3 py-2 "
                                   "text-sm font-semibold text-white shadow-sm sm:ml-3 sm:w-auto "
                                   (get-in type-dictionary [(:type @dialog-state) :button-class]))
                       :on-click (fn []
                                   ((:on-success @dialog-state))
                                   (rf/dispatch [:dialog->close]))}
              (if (:text-action-button @dialog-state)
                (:text-action-button @dialog-state)
                "Delete")]
             [:button {:type "button"
                       :class "mt-3 inline-flex w-full justify-center rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 hover:bg-gray-50 sm:mt-0 sm:w-auto"
                       :on-click #(rf/dispatch [:dialog->close])
                       :data-autofocus true}
              "Cancel"]]]]]]]])))
