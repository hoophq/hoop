(ns webapp.components.modal
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["@radix-ui/themes" :refer [Box Dialog VisuallyHidden Tooltip]]
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
      [:> Tooltip {:content "Close"}
       [:div {:class (str "absolute right-4 top-4 p-2 rounded-full bg-gray-100"
                          " hover:bg-gray-200 transition cursor-pointer z-10 group")
              :on-click modal-on-click-out}
        [:> hero-outline-icon/XMarkIcon {:class "h-5 w-5 text-gray-600"}]]]
      component]]))
(defmethod markup :default [_] nil)

(defn modal
  []
  (let [modal-status @(rf/subscribe [:modal])]
    [markup (:status modal-status) modal-status]))

(defn modal-radix []
  (let [modal (rf/subscribe [:modal-radix])]
    (fn []
      (let [on-click-out (if (:custom-on-click-out @modal)
                           #((:custom-on-click-out @modal))
                           #(rf/dispatch [:modal->close]))]
        (if (:open? @modal)
          ;; Let the app-level Theme provider handle the appearance
          [:> Box {:id (:id @modal)}
           [:> Dialog.Root {:open (:open? @modal)
                            :on-open-change #(rf/dispatch [:modal->set-status %])}
            [:> Dialog.Content {;;:maxWidth (or (:maxWidth @modal) "916px")
                                :maxHeight "calc(100vh - 96px)"
                                :on-escape-key-down on-click-out
                                :on-pointer-down-outside on-click-out
                                :class "p-0"}
             [:> VisuallyHidden :as-child true
              [:> Dialog.Title "Modal title"]]
             [:> VisuallyHidden :as-child true
              [:> Dialog.Description "Modal description"]]
             [:> Box {:p "5" :class (:class @modal)}
              (:content @modal)]]]]
          nil)))))
