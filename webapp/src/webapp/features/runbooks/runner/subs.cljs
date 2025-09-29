(ns webapp.features.runbooks.runner.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :runbooks/connection-dialog-open?
 (fn [db]
   (:connection-dialog-open? db)))

(rf/reg-sub
 :runbooks/execute-trigger
 (fn [db _]
   (get db :runbooks/execute-trigger false)))