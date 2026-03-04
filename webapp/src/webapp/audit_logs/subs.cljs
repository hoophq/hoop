(ns webapp.audit-logs.subs
  (:require
   [re-frame.core :as re-frame]))

(re-frame/reg-sub
 :audit-logs/data
 (fn [db _]
   (:audit-logs db)))

(re-frame/reg-sub
 :audit-logs/pagination
 (fn [db _]
   (get-in db [:audit-logs :pagination])))

(re-frame/reg-sub
 :audit-logs/filters
 (fn [db _]
   (get-in db [:audit-logs :filters])))

(re-frame/reg-sub
 :audit-logs/expanded-rows
 (fn [db _]
   (get-in db [:audit-logs :expanded-rows])))
