(ns webapp.components.virtualized-list-test
  "Above Chrome's layout clamp the scroll range maps onto the row range, so
  the last row stays reachable (SUP-278)."
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.components.virtualized-list :as vlist]))

(def ^:private small {:height 100 :item-height 20 :total 1000 :overscan 5})
(def ^:private huge {:height 100 :item-height 20 :total 2000000 :overscan 5})

(deftest small-list-keeps-rows-at-their-pixel-position
  (is (= {:start 0 :end 10 :offset-y 0 :total-height 20000}
         (vlist/window (assoc small :scroll-top 0))))
  (is (= {:start 45 :end 60 :offset-y 900 :total-height 20000}
         (vlist/window (assoc small :scroll-top 1000)))))

(deftest small-list-end-stays-within-total
  (let [{:keys [start end]} (vlist/window (assoc small :scroll-top 19900))]
    (is (= 990 start))
    (is (= 1000 end))))

(deftest huge-list-caps-the-scroll-box
  (is (= vlist/max-scroll-height
         (:total-height (vlist/window (assoc huge :scroll-top 0))))))

(deftest huge-list-starts-at-the-first-row
  (is (= {:start 0 :end 10 :offset-y 0}
         (dissoc (vlist/window (assoc huge :scroll-top 0)) :total-height))))

(deftest huge-list-reaches-the-last-row-at-max-scroll
  (let [{:keys [start end offset-y]} (vlist/window (assoc huge :scroll-top (- vlist/max-scroll-height 100)))]
    (is (= 2000000 end))
    (is (= (- 2000000 5 5) start))
    (is (= (- vlist/max-scroll-height 100 100) offset-y))))

(deftest huge-list-last-row-ends-on-the-viewport-bottom
  (let [height 354
        scroll-top (- vlist/max-scroll-height height)
        {:keys [start end offset-y]} (vlist/window (assoc huge :height height :scroll-top scroll-top))]
    (is (= 2000000 end))
    (is (= (+ scroll-top height) (+ offset-y (* (- end start) 20))))))

(deftest huge-list-rows-render-at-the-viewport
  (let [scroll-top 8000000
        {:keys [start end offset-y]} (vlist/window (assoc huge :scroll-top scroll-top))]
    (is (< 900000 start 1100000))
    (is (= (+ start 5 5 5) end))
    (is (= (- scroll-top 100) offset-y))))

(deftest empty-list
  (is (= {:start 0 :end 0 :offset-y 0 :total-height 0}
         (vlist/window {:scroll-top 0 :height 100 :item-height 20 :total 0 :overscan 5}))))
