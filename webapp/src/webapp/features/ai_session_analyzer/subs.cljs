(ns webapp.features.ai-session-analyzer.subs
  (:require
   [re-frame.core :as rf]))

;; Provider Subscriptions
(rf/reg-sub
 :ai-session-analyzer/provider
 (fn [db]
   (get-in db [:ai-session-analyzer :provider])))

(rf/reg-sub
 :ai-session-analyzer/provider-loading
 :<- [:ai-session-analyzer/provider]
 (fn [provider]
   (= (:status provider) :loading)))

(rf/reg-sub
 :ai-session-analyzer/provider-configured?
 :<- [:ai-session-analyzer/provider]
 (fn [provider]
   (and (= (:status provider) :success) (some? (:data provider)))))

(rf/reg-sub
 :ai-session-analyzer/rule-loading?
 (fn [db]
   (= (get-in db [:ai-session-analyzer :rule :status]) :loading)))

;; Rules Subscriptions
(rf/reg-sub
 :ai-session-analyzer/rules
 (fn [db]
   (get-in db [:ai-session-analyzer :rules])))

(rf/reg-sub
 :ai-session-analyzer/rules-loading
 :<- [:ai-session-analyzer/rules]
 (fn [rules]
   (= (:status rules) :loading)))

(rf/reg-sub
 :ai-session-analyzer/active-rule
 (fn [db]
   (get-in db [:ai-session-analyzer :active-rule])))
