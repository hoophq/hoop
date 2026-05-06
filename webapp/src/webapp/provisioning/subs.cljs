(ns webapp.provisioning.subs
  (:require [re-frame.core :as rf]))

(rf/reg-sub
 :provisioning/resources
 (fn [db]
   (get-in db [:provisioning :resources :data] [])))

(rf/reg-sub
 :provisioning/resources-status
 (fn [db]
   (get-in db [:provisioning :resources :status] :idle)))

(rf/reg-sub
 :provisioning/loading?
 :<- [:provisioning/resources-status]
 (fn [status]
   (= status :loading)))

(rf/reg-sub
 :provisioning/jobs
 (fn [db]
   (get-in db [:provisioning :jobs] [])))

(rf/reg-sub
 :provisioning/sessions
 (fn [db]
   (get-in db [:provisioning :sessions] [])))

(rf/reg-sub
 :provisioning/plan-job
 (fn [db]
   (get-in db [:provisioning :plan-job])))
