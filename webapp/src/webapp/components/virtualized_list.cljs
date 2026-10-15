(ns webapp.components.virtualized-list
  (:require
   [reagent.core :as r]))

(def ^:const max-scroll-height
  "Chrome clamps layout at 2^25 px (EVL-121). Above this the scroll box is
  scaled and the scroll range maps onto the row range."
  (* 16 1024 1024))

(defn window
  "The rows to render for a scroll position: {:start :end :offset-y
  :total-height}. `end` is exclusive."
  [{:keys [scroll-top height item-height total overscan]}]
  (let [real-height (* total item-height)
        total-height (min real-height max-scroll-height)
        scaled? (> real-height max-scroll-height)
        visible (js/Math.ceil (/ height item-height))
        first-row (if scaled?
                    (let [range (max 1 (- total-height height))
                          max-start (max 0 (- total visible))]
                      (js/Math.round (* (/ (min scroll-top range) range) max-start)))
                    (js/Math.floor (/ scroll-top item-height)))
        start (max 0 (min (- first-row overscan) (max 0 (dec total))))
        end (max start (min total (+ first-row visible overscan)))
        offset-y (cond
                   (not scaled?) (* start item-height)
                   ;; At the end of the range the last row sits on the bottom edge.
                   (= end total) (- (+ scroll-top height) (* (- end start) item-height))
                   :else (- scroll-top (* (- first-row start) item-height)))]
    {:start start :end end :offset-y offset-y :total-height total-height}))

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
              {safe-start :start safe-end :end
               :keys [offset-y total-height]} (window {:scroll-top @scroll-top
                                                       :height effective-height
                                                       :item-height item-height
                                                       :total total-items
                                                       :overscan overscan})

              visible-items (if (and (> total-items 0) (< safe-start total-items))
                              (subvec items safe-start safe-end)
                              [])

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
