(ns webapp.features.access-request.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :access-request/rules
 (fn [db]
   (get-in db [:access-request :rules])))

(rf/reg-sub
 :access-request/current-rule
 (fn [db]
   (get-in db [:access-request :current-rule])))

(rf/reg-sub
 :access-request/status
 (fn [db]
   (get-in db [:access-request :status])))
