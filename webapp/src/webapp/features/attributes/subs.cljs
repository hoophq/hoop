(ns webapp.features.attributes.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :attributes/list
 (fn [db]
   (get-in db [:attributes :list])))

(rf/reg-sub
 :attributes/list-data
 :<- [:attributes/list]
 (fn [list]
   (or (:data list) [])))

(rf/reg-sub
 :attributes/loading?
 :<- [:attributes/list]
 (fn [list]
   (= (:status list) :loading)))

(rf/reg-sub
 :attributes/active
 (fn [db]
   (get-in db [:attributes :active])))

(rf/reg-sub
 :attributes/submitting?
 (fn [db]
   (get-in db [:attributes :submitting?])))
