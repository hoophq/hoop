(ns webapp.features.promotion-test
  "The primary button sits inside the scrolling column, so a short window can
  still reach it (ENG-934)."
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.features.promotion :as promotion]))

(defn- class-of [node]
  (when (and (vector? node) (map? (get node 2)))
    (:class (get node 2))))

(defn- nodes [tree]
  (filter vector? (tree-seq coll? seq tree)))

(defn- has-text? [tree text]
  (some #(= text %) (tree-seq coll? seq tree)))

(def ^:private page
  (promotion/feature-promotion {:feature-name "Parallel Mode"
                                :mode :empty-state
                                :description "d"
                                :feature-items [{:icon [:span] :title "t" :description "d"}]
                                :on-primary-click identity
                                :primary-text "Get Started"}))

(deftest primary-button-is-inside-a-scrolling-column
  (let [scrolling (filter #(some-> (class-of %) (.includes "overflow-y-auto")) (nodes page))]
    (is (= 1 (count scrolling)))
    (is (has-text? (first scrolling) "Get Started"))))

(deftest no-column-centers-with-justify-center
  ;; justify-center overflows above the top edge, out of scroll reach.
  (is (not-any? #(some-> (class-of %) (.includes "justify-center")) (nodes page))))
