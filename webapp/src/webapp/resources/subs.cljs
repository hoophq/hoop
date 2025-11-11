(ns webapp.resources.subs
  (:require
   [re-frame.core :as rf]))

;; Resources pagination
(rf/reg-sub
 :resources->pagination
 (fn [db _]
   (:resources->pagination db)))

;; Resource details
(rf/reg-sub
 :resources->resource-details
 (fn [db _]
   (:resources->resource-details db)))

;; Resource roles
(rf/reg-sub
 :resources->resource-roles
 (fn [db [_ resource-id]]
   (get-in db [:resources->resource-roles resource-id])))

;; Resource updating state
(rf/reg-sub
 :resources->updating?
 (fn [db _]
   (get-in db [:resources->resource-details :updating?] false)))

