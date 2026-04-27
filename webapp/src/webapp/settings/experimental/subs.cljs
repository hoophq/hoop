(ns webapp.settings.experimental.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :settings-experimental/status
 (fn [db _]
   (get-in db [:settings-experimental :status])))

(rf/reg-sub
 :settings-experimental/flags
 (fn [db _]
   (get-in db [:settings-experimental :flags] [])))

(rf/reg-sub
 :settings-experimental/pending?
 (fn [db [_ flag-name]]
   (contains? (get-in db [:settings-experimental :pending] #{}) flag-name)))
