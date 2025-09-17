(ns webapp.components.draggable-card
  (:require ["lucide-react" :refer [Expand]]
            ["@radix-ui/themes" :refer [IconButton]]
            ["gsap/all" :refer [Draggable]]
            [re-frame.core :as rf]
            [reagent.core :as r]))

(defn- markup-draggable-card [_ _]
  (r/create-class {:display-name "draggable-card"
                   :component-did-mount #(.create Draggable ".draggable")
                   :reagent-render (fn [status {:keys [component on-click-expand]}]
                                     (if (= status :open)
                                       [:div {:class (str "draggable bg-white shadow-lg absolute bottom-10 "
                                                          "left-10 z-50 rounded-lg border border-gray-200 "
                                                          "overflow-auto p-radix-4 space-y-radix-4")}
                                        (when on-click-expand
                                          [:> IconButton {:size "2"
                                                          :variant "soft"
                                                          :color "gray"
                                                          :on-click on-click-expand}
                                           [:> Expand {:size 16}]])

                                        component]
                                       [:div {:class "draggable"}]))}))

(defn main []
  (let [card-options @(rf/subscribe [:draggable-card])]
    [markup-draggable-card (:status card-options) card-options]))
