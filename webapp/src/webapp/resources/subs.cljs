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

;; Metadata
(rf/reg-sub
 :resources->metadata
 (fn [db _]
   (get-in db [:resources :metadata :data])))

(rf/reg-sub
 :resources->metadata-loading?
 (fn [db _]
   (get-in db [:resources :metadata :loading] false)))

(rf/reg-sub
 :resources->metadata-error
 (fn [db _]
   (get-in db [:resources :metadata :error])))

;; Tags
(rf/reg-sub
 :resources->tags
 (fn [db]
   (get-in db [:resources :tags])))

(rf/reg-sub
 :resources->tags-loading?
 (fn [db]
   (get-in db [:resources :tags-loading])))

