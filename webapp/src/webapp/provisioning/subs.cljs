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
 :provisioning/plan-job
 (fn [db]
   (get-in db [:provisioning :plan-job])))

(rf/reg-sub
 :provisioning/sessions-cache
 (fn [db]
   (get-in db [:provisioning :sessions] [])))

(rf/reg-sub
 :provisioning/sessions
 :<- [:provisioning/sessions-cache]
 :<- [:provisioning/plan-job]
 ;; Apply re-issues a fresh sid on each plan-item (the apply transcript
 ;; replaces the plan dry-run for "View session" — see
 ;; :provisioning/apply-batch-response in events.cljs), so plan-phase
 ;; sessions linger in the cache after apply but are no longer referenced
 ;; by any item. Both phases also share the same :job-id, so a job-id
 ;; filter alone can't separate them. Surface only the sessions still
 ;; bound to a current plan-item :sid; this is the invariant the session
 ;; list relies on to avoid mixing plan and apply transcripts.
 (fn [[sessions plan-job] _]
   (let [item-sids (set (keep :sid (:items plan-job)))]
     (filterv #(contains? item-sids (:id %)) sessions))))
