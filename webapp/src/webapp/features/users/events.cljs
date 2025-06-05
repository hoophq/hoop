(ns webapp.features.users.events
  (:require
   [re-frame.core :as rf]))

;; Events específicos da feature users
;; Os events principais de usuários (create, update, get) já existem em webapp.events.users
;; Aqui adicionamos apenas events específicos da feature se necessário

(rf/reg-event-fx
 :users/set-invitation-mode
 (fn [{:keys [db]} [_ mode]]
   {:db (assoc-in db [:features :users :invitation-mode] mode)}))

(rf/reg-event-fx
 :users/clear-invitation-mode
 (fn [{:keys [db]} [_]]
   {:db (assoc-in db [:features :users :invitation-mode] nil)}))

(rf/reg-event-fx
 :users/mark-promotion-seen
 (fn [{:keys [db]} [_]]
   (.setItem (.-localStorage js/window) "users-promotion-seen" "true")
   {:db (assoc-in db [:features :users :promotion-seen] true)}))
