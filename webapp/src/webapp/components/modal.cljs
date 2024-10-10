(ns webapp.components.modal
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["@radix-ui/themes" :refer [Box Dialog VisuallyHidden ScrollArea]]
            [re-frame.core :as rf]))

(defmulti markup identity)
(defmethod markup :open [_ {:keys [size component on-click-out]}]
  (let [modal-on-click-out (if (nil? on-click-out)
                             #(rf/dispatch [:close-modal])
                             #(on-click-out))
        modal-size (if (= size :large)
                     "w-full max-w-xs lg:max-w-4xl" "max-w-xs lg:max-w-lg w-full")]
    [:div {:id "modal"
           :class "fixed z-50 inset-0 overflow-y-auto"
           "aria-modal" true}
     [:div
      {"aria-hidden" "true"
       :class "fixed w-full h-full inset-0 bg-black bg-opacity-80 transition"
       :on-click modal-on-click-out}]
     [:div
      {:class (str "relative mb-large m-auto "
                   "bg-white shadow-sm rounded-lg "
                   "mx-auto mt-16 lg:mt-large p-regular overflow-auto "
                   modal-size)}
      [:div {:class (str "absolute right-4 top-4 p-2 rounded-full bg-gray-100"
                         " hover:bg-gray-200 transition cursor-pointer z-10 group")
             :on-click modal-on-click-out}
       [:div {:class "absolute -bottom-10 left-1/2 flex-col hidden mt-6 w-max group-hover:flex items-center -translate-x-1/2"}
        [:div {:class "w-3 h-3 -mb-2 bg-gray-900 transform rotate-45"}]
        [:span {:class (str "relative bg-gray-900 rounded-md z-50 "
                            "py-1.5 px-3.5 text-xs text-white leading-none whitespace-no-wrap shadow-lg")}
         "Close"]]
       [:> hero-outline-icon/XMarkIcon {:class "h-5 w-5 text-gray-600"}]]
      component]]))
(defmethod markup :default [_] nil)

(defn modal
  []
  (let [modal-status @(rf/subscribe [:modal])]
    [markup (:status modal-status) modal-status]))

(defn modal-radix []
  (let [modal (rf/subscribe [:modal-radix])]
    (fn []
      (if (:open? @modal)

        [:> Dialog.Root {:open (:open? @modal)
                         :on-open-change #(rf/dispatch [:modal->set-status %])}

         [:> Dialog.Content {:maxWidth "916px"
                             :maxHeight "calc(100vh - 96px)"
                             :on-escape-key-down #(rf/dispatch [:modal->close])
                             :on-pointer-down-outside #(rf/dispatch [:modal->close])
                             :class "p-0"}
          [:> VisuallyHidden :as-child true
           [:> Dialog.Title "Modal title"]]
          [:> VisuallyHidden :as-child true
           [:> Dialog.Description "Modal description"]]
          [:> Box {:p "6"}
           (:content @modal)]]]
        nil))))
