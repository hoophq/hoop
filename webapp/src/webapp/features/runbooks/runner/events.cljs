(ns webapp.features.runbooks.runner.events
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-db
 :runbooks/toggle-connection-dialog
 (fn [db [_ open?]]
   (assoc db :connection-dialog-open? open?)))

(rf/reg-event-db
 :runbooks/trigger-execute
 (fn [db _]
   (assoc db :runbooks/execute-trigger true)))

(rf/reg-event-db
 :runbooks/execute-handled
 (fn [db _]
   (assoc db :runbooks/execute-trigger false)))
