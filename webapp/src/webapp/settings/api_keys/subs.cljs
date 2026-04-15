(ns webapp.settings.api-keys.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :api-keys/list
 (fn [db]
   (get-in db [:api-keys :list])))

(rf/reg-sub
 :api-keys/list-data
 :<- [:api-keys/list]
 (fn [list]
   (or (:data list) [])))

(rf/reg-sub
 :api-keys/loading?
 :<- [:api-keys/list]
 (fn [list]
   (= (:status list) :loading)))

(rf/reg-sub
 :api-keys/active
 (fn [db]
   (get-in db [:api-keys :active])))

(rf/reg-sub
 :api-keys/created
 (fn [db]
   (get-in db [:api-keys :created])))

(rf/reg-sub
 :api-keys/submitting?
 (fn [db]
   (get-in db [:api-keys :submitting?])))
