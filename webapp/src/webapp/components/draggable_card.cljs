(ns webapp.components.draggable-card
  (:require ["@heroicons/react/20/solid" :as hero-solid-icon]
            ["gsap/all" :refer [Draggable]]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.icon :as icon]))

(defn- markup-draggable-card [_ _]
  (r/create-class {:display-name "draggable-card"
                   :component-did-mount #(.create Draggable ".draggable")
                   :reagent-render (fn [status {:keys [component on-click-close on-click-expand]}]
                                     (if (= status :open)
                                       [:div {:class "draggable bg-white shadow-lg absolute bottom-10 left-10 z-50 rounded-lg border border-gray-200 overflow-auto py-small"}
                                        [:div {:class "flex items-center gap-small px-small pb-3"}
                                         (when on-click-expand
                                           [:div {:class (str "rounded-full bg-gray-100"
                                                              " hover:bg-gray-200 transition cursor-pointer")
                                                  :on-click on-click-expand}
                                            [:div {:class "p-2"}
                                             [:> hero-solid-icon/ArrowsPointingOutIcon {:class "h-5 w-5"}]]])
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
