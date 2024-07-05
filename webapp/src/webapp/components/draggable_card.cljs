(ns webapp.components.draggable-card
  (:require ["gsap/all" :refer [Draggable]]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.icon :as icon]))

(defn- markup-draggable-card [_ _]
  (r/create-class {:display-name "draggable-card"
                   :component-did-mount #(.create Draggable ".draggable")
                   :reagent-render (fn [status {:keys [component on-click-close on-click-expand]}]
                                     (if (= status :open)
                                       [:div {:class "draggable bg-white shadow-lg absolute bottom-10 left-10 z-50 rounded-lg border border-gray-200 overflow-auto pt-small pb-regular"}
                                        [:div {:class "flex justify-end items-center gap-small px-small"}
                                         (when on-click-expand
                                           [:div {:class (str "rounded-full bg-gray-100"
                                                              " hover:bg-gray-200 transition cursor-pointer")
                                                  :on-click on-click-expand}
                                            [:div {:class "p-0.5"}
                                             [icon/regular {:size "4"
                                                            :icon-name "arrows-pointing-out"}]]])
                                         (when on-click-close
                                           [:div {:class (str "rounded-full bg-gray-100"
                                                              " hover:bg-gray-200 transition cursor-pointer")
                                                  :on-click on-click-close}
                                            [:div {:class "p-0.5"}
                                             [icon/regular {:size "4"
                                                            :icon-name "close"}]]])]
                                        [:div {:class "px-regular"}
                                         component]]
                                       [:div {:class "draggable"}]))}))

(defn main []
  (let [card-options @(rf/subscribe [:draggable-card])]
    [markup-draggable-card (:status card-options) card-options]))

;; --------- Modal of draggable Card

(defmulti markup-modal identity)
(defmethod markup-modal :open [_ {:keys [size component on-click-out]}]
  (let [modal-on-click-out (if (nil? on-click-out)
                             #(rf/dispatch [:close-modal])
                             #(on-click-out))
        modal-size (if (= size :large)
                     "w-full max-w-4xl" "max-w-lg w-full")]
    [:div {:id "modal-draggable-card"
           :class "fixed z-20 inset-0 overflow-y-auto"
           "aria-modal" true}
     [:div
      {"aria-hidden" "true"
       :class "fixed w-full h-full inset-0 bg-black bg-opacity-80 transition"
       :on-click modal-on-click-out}]
     [:div
      {:class (str "relative mb-large m-auto "
                   "bg-white shadow-sm rounded-lg "
                   "mx-auto mt-large p-regular overflow-auto "
                   modal-size)}
      [:div
       {:class (str "absolute right-4 top-4 rounded-full bg-gray-100"
                    " hover:bg-gray-200 transition cursor-pointer z-50")
        :on-click modal-on-click-out}
       [:div {:class "p-0.5"}
        [icon/regular {:size "6"
                       :icon-name "close"}]]]
      component]]))
(defmethod markup-modal :default [_] nil)

(defn modal []
  (let [modal-status @(rf/subscribe [:draggable-card->modal])]
    [markup-modal (:status modal-status) modal-status]))
