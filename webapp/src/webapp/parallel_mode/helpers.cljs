(ns webapp.parallel-mode.helpers
  (:require [webapp.parallel-mode.db :as db]))

;; ---- Connection Filtering ----

(defn valid-for-parallel?
  "Check if connection is valid for parallel mode execution"
  [connection]
  (and
   ;; Exclude specific subtypes
   (not (contains? db/excluded-subtypes (:subtype connection)))
   ;; Must have exec enabled
   (= "enabled" (:access_mode_exec connection))
   ;; Must be online
   (= "online" (:status connection))))

(defn filter-valid-connections
  "Filter connections that are valid for parallel mode"
  [connections]
  (filterv valid-for-parallel? connections))

;; ---- Selection Helpers ----

(defn connection-selected?
  "Check if a connection is in the selected list"
  [connection selected-connections]
  (some #(= (:name %) (:name connection)) selected-connections))

(defn toggle-in-collection
  "Toggle an item in a collection based on a predicate"
  [coll item pred]
  (if (some #(pred % item) coll)
    (filterv #(not (pred % item)) coll)
    (conj coll item)))

;; ---- Validation ----

(defn has-minimum-connections?
  "Check if we have at least the minimum required connections"
  [selected-connections]
  (>= (count selected-connections) db/min-connections))

;; ---- Persistence Helpers ----

(defn connections->storage-format
  "Convert connections to format suitable for localStorage"
  [connections]
  (mapv #(select-keys % [:name]) connections))
