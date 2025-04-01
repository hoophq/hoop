(ns webapp.connections.views.setup.events.ssh
  (:require [re-frame.core :as rf]))

;; Events
(rf/reg-event-db
 :connection-setup/update-ssh-credentials
 (fn [db [_ key value]]
   (assoc-in db [:connection-setup :ssh-credentials key] value)))

(rf/reg-event-db
 :connection-setup/clear-ssh-credentials
 (fn [db _]
   (assoc-in db [:connection-setup :ssh-credentials] {})))

;; Subscriptions
(rf/reg-sub
 :connection-setup/ssh-credentials
 (fn [db]
   (get-in db [:connection-setup :ssh-credentials] {})))
