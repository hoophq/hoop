(ns webapp.components.virtualized-list
  (:require
   [reagent.core :as r]))

(defn virtualized-list
  "Virtualized list that renders only visible items

   Props:
   - items: vector of items to render
   - item-height: fixed height of each item in pixels
   - container-height: height of the container in pixels
   - render-item: function (fn [item index]) that returns the item component
   - overscan: number of extra items to render (default: 5)"

  []
  (let [scroll-top (r/atom 0)
        prev-items-count (r/atom 0)]

    (fn [{:keys [items item-height container-height render-item overscan]
          :or {overscan 5}}]
      (let [total-items (count items)

            ;; reset scroll position if items changed significantly (e.g., search)
            _ (when (not= total-items @prev-items-count)
                (reset! scroll-top 0)
                (reset! prev-items-count total-items))

            visible-count (js/Math.ceil (/ container-height item-height))
            start-index (js/Math.max 0 (- (js/Math.floor (/ @scroll-top item-height)) overscan))
            end-index (js/Math.min total-items (+ start-index visible-count (* 2 overscan)))

            ;; ensure that the indices are valid
            safe-start (js/Math.max 0 (js/Math.min start-index (js/Math.max 0 (- total-items 1))))
            safe-end (js/Math.max safe-start (js/Math.min end-index total-items))

            visible-items (if (and (> total-items 0) (< safe-start total-items))
                            (subvec items safe-start safe-end)
                            [])

            ;; calculate position of the first item
            total-height (* total-items item-height)
            offset-y (* safe-start item-height)]

        [:div
         {:class "relative overflow-auto rounded-lg"
          :style {:max-height (str container-height "px")}
          :ref (fn [el]
                 ;; reset scroll position when component updates with different items
                 (when (and el (= @scroll-top 0))
                   (set! (.-scrollTop el) 0)))
          :on-scroll (fn [e]
                       (reset! scroll-top (.. e -target -scrollTop)))}

         ;; invisible container to keep the scroll correct
         [:div {:style {:height (str total-height "px")
                        :position "relative"}}

          ;; visible items container
          [:div {:style {:position "absolute"
                         :top (str offset-y "px")
                         :left "0"
                         :right "0"}}

           ;; render only visible items
           (for [i (range (count visible-items))]
             (let [item (nth visible-items i)
                   actual-index (+ safe-start i)]
               ^{:key actual-index}
               [render-item item actual-index]))]]]))))
