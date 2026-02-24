(ns webapp.webclient.components.database-schema-tree-navigation
  "ARIA Tree navigation utilities for keyboard accessibility"
  (:require [clojure.string :as cs]))

;; Atom to track currently focused tree item
(def focused-item-id (atom nil))

(defn generate-tree-item-id
  "Generates a unique ID for a tree item"
  [& parts]
  (cs/join "-" (map str parts)))

(defn get-all-visible-items
  "Gets all currently visible tree items in the DOM"
  []
  (array-seq (.querySelectorAll js/document "[role='treeitem'][data-tree-item-id]")))

(defn find-item-index
  "Finds the index of an item in the visible items list"
  [items item-id]
  (let [items-vec (vec items)]
    (first (keep-indexed #(when (= (.getAttribute %2 "data-tree-item-id") item-id) %1) items-vec))))

(defn focus-item
  "Focuses a tree item and updates tabindex"
  [item-id]
  (when-let [elem (.querySelector js/document (str "[data-tree-item-id='" item-id "']"))]
    ;; Remove tabindex from previously focused item
    (when @focused-item-id
      (when-let [prev-elem (.querySelector js/document (str "[data-tree-item-id='" @focused-item-id "']"))]
        (.setAttribute prev-elem "tabIndex" "-1")))
    
    ;; Set new focused item
    (.setAttribute elem "tabIndex" "0")
    (.focus elem)
    (reset! focused-item-id item-id)))

(defn handle-arrow-down
  "Navigate to next visible item"
  [current-id]
  (let [items (get-all-visible-items)
        current-idx (find-item-index items current-id)]
    (when (and current-idx (< current-idx (dec (count items))))
      (let [next-item (nth items (inc current-idx))
            next-id (.getAttribute next-item "data-tree-item-id")]
        (focus-item next-id)
        true))))

(defn handle-arrow-up
  "Navigate to previous visible item"
  [current-id]
  (let [items (get-all-visible-items)
        current-idx (find-item-index items current-id)]
    (when (and current-idx (> current-idx 0))
      (let [prev-item (nth items (dec current-idx))
            prev-id (.getAttribute prev-item "data-tree-item-id")]
        (focus-item prev-id)
        true))))

(defn handle-arrow-right
  "Expand collapsed item or move to first child if already expanded"
  [current-id on-expand]
  (when-let [elem (.querySelector js/document (str "[data-tree-item-id='" current-id "']"))]
    (let [is-expanded (= (.getAttribute elem "aria-expanded") "true")]
      (if is-expanded
        ;; If already expanded, move to first child
        (let [items (get-all-visible-items)
              current-idx (find-item-index items current-id)]
          (when (and current-idx (< current-idx (dec (count items))))
            (let [next-item (nth items (inc current-idx))
                  next-id (.getAttribute next-item "data-tree-item-id")]
              (focus-item next-id))))
        ;; If collapsed, expand it
        (when on-expand (on-expand)))
      true)))

(defn handle-arrow-left
  "Collapse expanded item or move to parent if already collapsed"
  [current-id on-collapse parent-id]
  (when-let [elem (.querySelector js/document (str "[data-tree-item-id='" current-id "']"))]
    (let [is-expanded (= (.getAttribute elem "aria-expanded") "true")]
      (if is-expanded
        ;; If expanded, collapse it
        (when on-collapse (on-collapse))
        ;; If collapsed, move to parent
        (when parent-id (focus-item parent-id)))
      true)))

(defn handle-home
  "Navigate to first item in tree"
  []
  (let [items (get-all-visible-items)]
    (when (seq items)
      (let [first-item (first items)
            first-id (.getAttribute first-item "data-tree-item-id")]
        (focus-item first-id)
        true))))

(defn handle-end
  "Navigate to last visible item in tree"
  []
  (let [items (get-all-visible-items)]
    (when (seq items)
      (let [last-item (last items)
            last-id (.getAttribute last-item "data-tree-item-id")]
        (focus-item last-id)
        true))))

(defn create-tree-item-props
  "Creates common props for a tree item with keyboard navigation"
  [{:keys [item-id
           on-expand
           on-collapse
           parent-id
           level
           setsize
           posinset
           is-expanded
           aria-label]}]
  {:data-tree-item-id item-id
   :tabIndex (if (= @focused-item-id item-id) "0" "-1")
   :aria-level (str level)
   :aria-setsize (when setsize (str setsize))
   :aria-posinset (when posinset (str posinset))
   :aria-expanded (when (some? is-expanded) (if is-expanded "true" "false"))
   :aria-label (or aria-label "")
   :on-key-down (fn [e]
                  (let [key (.-key e)
                        handled (case key
                                  "ArrowDown" (handle-arrow-down item-id)
                                  "ArrowUp" (handle-arrow-up item-id)
                                  "ArrowRight" (handle-arrow-right item-id on-expand)
                                  "ArrowLeft" (handle-arrow-left item-id on-collapse parent-id)
                                  "Home" (handle-home)
                                  "End" (handle-end)
                                  "Enter" (do (when on-expand (on-expand)) true)
                                  " " (do (when on-expand (on-expand)) true)
                                  false)]
                    (when handled
                      (.preventDefault e)
                      (.stopPropagation e))))
   :on-focus (fn [_]
               (reset! focused-item-id item-id))})
