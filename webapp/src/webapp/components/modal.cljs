(ns webapp.components.modal
  (:require [re-frame.core :as rf]
            [webapp.components.icon :as icon]))

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
      [:div
       {:class (str "absolute right-4 top-4 rounded-full bg-gray-100"
                    " hover:bg-gray-200 transition cursor-pointer z-10")
        :on-click modal-on-click-out}
       [:div {:class "p-0.5"}
        [icon/regular {:size "6"
                       :icon-name "close"}]]]
      component]]))
(defmethod markup :default [_] nil)

(defn modal
  []
  (let [modal-status @(rf/subscribe [:modal])]
    [markup (:status modal-status) modal-status]))
