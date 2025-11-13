(ns webapp.features.runbooks.setup.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :runbooks/plugin
 (fn [db]
   (get-in db [:plugins->plugin-details :plugin])))

(rf/reg-sub
 :runbooks/connections
 :<- [:runbooks/plugin]
 (fn [plugin]
   (:connections plugin)))

(rf/reg-sub
 :runbooks/paths-by-connection
 :<- [:runbooks/connections]
 (fn [connections]
   (when connections
     (reduce (fn [acc conn]
               (let [paths (or (:config conn) [])]
                 (assoc acc (:id conn) paths)))
             {}
             (or connections [])))))

;; Runbooks Rules Subscriptions
(rf/reg-sub
 :runbooks-rules/list
 (fn [db]
   (get-in db [:runbooks-rules :list])))

(rf/reg-sub
 :runbooks-rules/list-loading
 :<- [:runbooks-rules/list]
 (fn [list]
   (= (:status list) :loading)))

(rf/reg-sub
 :runbooks-rules/active-rule
 (fn [db]
   (get-in db [:runbooks-rules :active-rule])))

;; Runbooks Configuration Subscriptions
(rf/reg-sub
 :runbooks-configurations/data
 (fn [db]
   (get-in db [:runbooks-configurations])))

(rf/reg-sub
 :runbooks-configurations/data-loading
 :<- [:runbooks-configurations/data]
 (fn [data]
   (= (:status data) :loading)))

;; Runbooks List Subscriptions
(rf/reg-sub
 :runbooks/list
 (fn [db]
   (get-in db [:runbooks :list])))
