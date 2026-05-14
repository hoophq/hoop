(ns webapp.features.rulepacks.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :rulepacks/list
 (fn [db _]
   (get-in db [:rulepacks :list :data])))

(rf/reg-sub
 :rulepacks/list-status
 (fn [db _]
   (get-in db [:rulepacks :list :status])))

(rf/reg-sub
 :rulepacks/list-search
 (fn [db _]
   (get-in db [:rulepacks :list :search] "")))

(rf/reg-sub
 :rulepacks/active
 (fn [db _]
   (get-in db [:rulepacks :active :data])))

(rf/reg-sub
 :rulepacks/active-status
 (fn [db _]
   (get-in db [:rulepacks :active :status])))

(rf/reg-sub
 :rulepacks/selected-connections
 (fn [db _]
   (get-in db [:rulepacks :selected-connections] #{})))

(rf/reg-sub
 :rulepacks/applying?
 (fn [db _]
   (get-in db [:rulepacks :applying?])))

(rf/reg-sub
 :rulepacks/has-pending-changes?
 :<- [:rulepacks/active]
 :<- [:rulepacks/selected-connections]
 (fn [[active selected] _]
   (let [saved (set (or (:connection_names active) []))]
     (not= saved selected))))

(rf/reg-sub
 :rulepacks/connections
 (fn [db _]
   (get-in db [:connections->list :data])))

(rf/reg-sub
 :rulepacks/connections-status
 (fn [db _]
   (get-in db [:connections->list :status])))

(rf/reg-sub
 :rulepacks/enabled?
 (fn [db _]
   (let [flags (get-in db [:settings-experimental :flags] [])
         match (first (filter #(= "experimental.rulepacks" (:name %)) flags))]
     (boolean (:enabled match)))))
