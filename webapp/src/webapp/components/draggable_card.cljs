(ns webapp.components.draggable-card
  (:require ["lucide-react" :refer [Expand]]
            ["@radix-ui/themes" :refer [IconButton]]
            ["gsap/all" :refer [Draggable]]
            [re-frame.core :as rf]
            [reagent.core :as r]))

(defn- calculate-card-position
  "Calculate initial position for card to avoid overlap"
  [index _total]
  (let [base-bottom 40
        base-left 40
        offset-x 60
        offset-y 60]
    {:bottom (+ base-bottom (* index offset-y))
     :left (+ base-left (* index offset-x))}))

(defn- markup-single-draggable-card [connection-name _card-data index total]
  (r/create-class
   {:display-name (str "draggable-card-" connection-name)
    :component-did-mount
    (fn []
      (let [selector (str ".draggable-" connection-name)]
        (.create Draggable selector)))

    :reagent-render
    (fn [connection-name {:keys [component on-click-expand status]} _index _total]
      (if (= status :open)
        (let [position (calculate-card-position index total)]
          [:div {:class (str "draggable-" connection-name
                             " bg-white shadow-lg absolute z-50 rounded-5 "
                             "border border-gray-200 overflow-auto p-radix-4 space-y-radix-4")
                 :style {:bottom (str (:bottom position) "px")
                         :left (str (:left position) "px")}}
           (when on-click-expand
             [:> IconButton {:size "2"
                             :variant "soft"
                             :color "gray"
                             :on-click on-click-expand}
              [:> Expand {:size 16}]])

           component])
        [:div {:class (str "draggable-" connection-name)}]))}))

(defn multiple-cards
  "Render multiple draggable cards"
  []
  (let [cards @(rf/subscribe [:draggable-cards])
        cards-vec (vec cards)
        total (count cards-vec)]
    [:div
     (doall
      (map-indexed
       (fn [idx [connection-name card-data]]
         ^{:key connection-name}
         [markup-single-draggable-card connection-name card-data idx total])
       cards-vec))]))

;; Legacy single card component (for backward compatibility)
(defn- markup-draggable-card [_ _]
  (r/create-class {:display-name "draggable-card"
                   :component-did-mount #(.create Draggable ".draggable")
                   :reagent-render (fn [status {:keys [component on-click-expand]}]
                                     (if (= status :open)
                                       [:div {:class (str "draggable bg-white shadow-lg absolute bottom-10 "
                                                          "left-10 z-50 rounded-5 border border-gray-200 "
                                                          "overflow-auto p-radix-4 space-y-radix-4")}
                                        (when on-click-expand
                                          [:> IconButton {:size "2"
                                                          :variant "soft"
                                                          :color "gray"
                                                          :on-click on-click-expand}
                                           [:> Expand {:size 16}]])

                                        component]
                                       [:div {:class "draggable"}]))}))

(defn main
  "Main component that renders both legacy single card and new multiple cards"
  []
  (let [card-options @(rf/subscribe [:draggable-card])
        multiple-cards-data @(rf/subscribe [:draggable-cards])]
    [:<>
     ;; Legacy single card (if exists)
     (when (= (:status card-options) :open)
       [markup-draggable-card (:status card-options) card-options])

     ;; New multiple cards system
     (when (seq multiple-cards-data)
       [multiple-cards])]))
