(ns webapp.features.runbooks.runner.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :runbooks/connection-dialog-open?
 (fn [db]
   (get-in db [:runbooks :connection-dialog-open?])))

(rf/reg-sub
 :runbooks/execute-trigger
 (fn [db _]
   (get-in db [:runbooks :execute-trigger] false)))

(rf/reg-sub
 :runbooks/selected-connection
 (fn [db]
   (get-in db [:runbooks :selected-connection])))
