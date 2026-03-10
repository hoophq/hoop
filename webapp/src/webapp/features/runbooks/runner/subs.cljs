(ns webapp.features.runbooks.runner.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :runbooks/connection-dialog-open?
 (fn [db]
   (get-in db [:runbooks :connection-dialog-open?])))


(rf/reg-sub
 :runbooks/selected-connection
 (fn [db]
   (get-in db [:runbooks :selected-connection])))

(rf/reg-sub
 :runbooks/execution-requirements-callout-dismissed?
 (fn [db]
   (get-in db [:runbooks :execution-requirements-callout :dismissed?] false)))
