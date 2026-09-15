;; See the note in events.cljs: only the read path survives the React
;; migration.
(ns webapp.features.ai-session-analyzer.subs
  (:require
   [re-frame.core :as rf]))

;; Provider Subscriptions
(rf/reg-sub
 :ai-session-analyzer/provider
 (fn [db]
   (get-in db [:ai-session-analyzer :provider])))

(rf/reg-sub
 :ai-session-analyzer/role-rule
 (fn [db]
   (get-in db [:ai-session-analyzer :role-rule])))

(rf/reg-sub
 :ai-session-analyzer/role-has-rule?
 :<- [:ai-session-analyzer/role-rule]
 (fn [role-rule]
   (and (= (:status role-rule) :success)
        (some? (:data role-rule)))))

;; Rules Subscriptions
(rf/reg-sub
 :ai-session-analyzer/rules
 (fn [db]
   (get-in db [:ai-session-analyzer :rules])))
