(ns webapp.features.runbooks.setup.subs
  (:require
   [re-frame.core :as rf]))

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

;; Subscription for runner - transforms list data to runner format
(rf/reg-sub
 :runbooks/runner-data
 :<- [:runbooks/list]
 :<- [:runbooks/selected-connection]
 (fn [[list-data]]
   (if (nil? list-data)
     {:status :idle :data {:repositories [] :items []} :error nil}
     (let [status (:status list-data)
           repositories (or (:data list-data) [])
           all-items (mapcat :items repositories)]
       {:status status
        :data {:repositories repositories
               :items all-items}
        :error (:error list-data)}))))
