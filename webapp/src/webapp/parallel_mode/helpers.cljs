(ns webapp.parallel-mode.helpers
  (:require [clojure.string :as cs]
            [webapp.parallel-mode.db :as db]))

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

;; ---- Pre-validation Helpers ----

(defn has-jira-template?
  "Check if connection has Jira template configured"
  [connection]
  (not (cs/blank? (:jira_issue_template_id connection))))

(defn has-required-metadata?
  "Check if connection requires metadata"
  [connection]
  (boolean (seq (:required_metadata connection))))

(defn pre-validate-connection
  "Pre-validate a connection and return error status if invalid for parallel mode"
  [connection jira-enabled?]
  (cond
    (and (has-jira-template? connection) jira-enabled?)
    :error-jira-template
    
    (has-required-metadata? connection)
    :error-metadata-required
    
    :else
    nil))

(defn split-by-validation
  "Split connections into valid (to execute) and invalid (pre-failed)"
  [connections jira-enabled?]
  (let [with-validation (map (fn [conn]
                               (assoc conn :pre-validation-error
                                      (pre-validate-connection conn jira-enabled?)))
                             connections)
        to-execute (filterv #(nil? (:pre-validation-error %)) with-validation)
        pre-failed (filterv #(some? (:pre-validation-error %)) with-validation)]
    {:to-execute to-execute
     :pre-failed pre-failed}))

;; ---- Persistence Helpers ----

(defn connections->storage-format
  "Convert connections to format suitable for localStorage"
  [connections]
  (mapv #(select-keys % [:name]) connections))
