(ns webapp.components.virtualized-list
  (:require
   [reagent.core :as r]))

(defn virtualized-list
  "Virtualized list that renders only visible items.

   Props:
   - items: vector of items to render
   - item-height: fixed height of each item in pixels
   - container-height: optional fixed container height in pixels. When omitted,
     the list fills its parent and measures itself via ResizeObserver so the
     visible window matches the actual rendered area.
   - render-item: function (fn [item index]) that returns the item component
   - overscan: number of extra items to render (default: 5)"

  []
  (let [scroll-top (r/atom 0)
        measured-height (r/atom nil)
        prev-items-count (r/atom 0)
        container-ref (atom nil)
        observer (atom nil)
        attach-observer!
        (fn [el]
          (reset! container-ref el)
          (when (and el (nil? @observer))
            (let [h (.-clientHeight el)]
              (when (and h (pos? h) (not= h @measured-height))
                (reset! measured-height h)))
            (when (.-ResizeObserver js/window)
              (let [obs (js/ResizeObserver.
                         (fn [entries]
                           (when-let [entry (first (array-seq entries))]
                             (let [h (.. entry -contentRect -height)]
                               (when (and h (not= h @measured-height))
                                 (reset! measured-height h))))))]
                (.observe obs el)
                (reset! observer obs)))))]

    (r/create-class
     {:display-name "virtualized-list"

      :component-will-unmount
      (fn [_]
        (when-let [obs @observer]
          (.disconnect obs)
          (reset! observer nil)))

      :reagent-render
      (fn [{:keys [items item-height container-height render-item overscan]
            :or {overscan 5}}]
        (let [total-items (count items)

              _ (when (not= total-items @prev-items-count)
                  (reset! scroll-top 0)
                  (reset! prev-items-count total-items))

              effective-height (or @measured-height container-height 0)
              visible-count (js/Math.ceil (/ effective-height item-height))
              start-index (js/Math.max 0 (- (js/Math.floor (/ @scroll-top item-height)) overscan))
              end-index (js/Math.min total-items (+ start-index visible-count (* 2 overscan)))

              safe-start (js/Math.max 0 (js/Math.min start-index (js/Math.max 0 (- total-items 1))))
              safe-end (js/Math.max safe-start (js/Math.min end-index total-items))

              visible-items (if (and (> total-items 0) (< safe-start total-items))
                              (subvec items safe-start safe-end)
                              [])

              total-height (* total-items item-height)
              offset-y (* safe-start item-height)
              fixed-style (when container-height
                            {:max-height (str container-height "px")})]

          [:div
           {:class "relative overflow-auto rounded-lg h-full w-full"
            :style fixed-style
            :ref attach-observer!
            :on-scroll (fn [e]
                         (reset! scroll-top (.. e -target -scrollTop)))}

           [:div {:style {:height (str total-height "px")
                          :position "relative"}}

            [:div {:style {:position "absolute"
                           :top (str offset-y "px")
                           :left "0"
                           :right "0"}}

             (for [i (range (count visible-items))]
               (let [item (nth visible-items i)
                     actual-index (+ safe-start i)]
                 ^{:key actual-index}
                 [render-item item actual-index]))]]]))})))
