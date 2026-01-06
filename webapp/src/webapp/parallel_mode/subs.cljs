(ns webapp.parallel-mode.subs
  (:require
   [re-frame.core :as rf]
   [webapp.parallel-mode.helpers :as helpers]))

;; ---- Modal Subscriptions ----

(rf/reg-sub
 :parallel-mode/modal-open?
 (fn [db _]
   (get-in db [:parallel-mode :modal :open?] false)))

(rf/reg-sub
 :parallel-mode/search-term
 (fn [db _]
   (get-in db [:parallel-mode :modal :search-term] "")))

;; ---- Selection Subscriptions ----

(rf/reg-sub
 :parallel-mode/selected-connections
 (fn [db _]
   (get-in db [:parallel-mode :selection :connections] [])))

(rf/reg-sub
 :parallel-mode/selected-count
 :<- [:parallel-mode/selected-connections]
 (fn [connections _]
   (count connections)))

(rf/reg-sub
 :parallel-mode/has-minimum?
 :<- [:parallel-mode/selected-connections]
 (fn [connections _]
   (helpers/has-minimum-connections? connections)))

(rf/reg-sub
 :parallel-mode/is-active?
 :<- [:parallel-mode/selected-connections]
 (fn [connections _]
   (helpers/has-minimum-connections? connections)))

;; ---- Connections Data Subscriptions ----

(rf/reg-sub
 :parallel-mode/valid-connections
 :<- [:connections->pagination]
 (fn [connections _]
   (let [all-connections (or (:data connections) [])]
     (helpers/filter-valid-connections all-connections))))

