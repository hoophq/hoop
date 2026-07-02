(ns webapp.connections.views.setup.events.ssh
  (:require [re-frame.core :as rf]))

;; Events
(rf/reg-event-db
 :connection-setup/update-ssh-credentials
 (fn [db [_ key value]]
   (assoc-in db [:connection-setup :ssh-credentials key] value)))

;; ssh-connection-type selects how the SSH session is served:
;;   "proxy" (default) — the agent authenticates to a remote SSH server and
;;                       forwards the session (wire subtype "ssh").
;;   "local"           — the agent runs the shell/command directly on its own
;;                       host; no target host or credentials (wire subtype
;;                       "ssh-local").
(rf/reg-event-db
 :connection-setup/set-ssh-connection-type
 (fn [db [_ conn-type]]
   (assoc-in db [:connection-setup :ssh-connection-type] conn-type)))

;; Subscriptions
(rf/reg-sub
 :connection-setup/ssh-credentials
 (fn [db]
   (get-in db [:connection-setup :ssh-credentials] {})))

(rf/reg-sub
 :connection-setup/ssh-connection-type
 (fn [db]
   (get-in db [:connection-setup :ssh-connection-type] "proxy")))
