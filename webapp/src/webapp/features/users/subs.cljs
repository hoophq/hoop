(ns webapp.features.users.subs
  (:require
   [re-frame.core :as rf]))

;; Subscriptions específicas da feature users
;; As subs principais de usuários (:users, :user-groups, :users->current-user) já existem em webapp.subs
;; Aqui adicionamos apenas subs específicas da feature se necessário

(rf/reg-sub
 :users/invitation-mode
 (fn [db]
   (get-in db [:features :users :invitation-mode])))

(rf/reg-sub
 :users/should-show-promotion
 :<- [:users]
 (fn [users]
   (= (count users) 1)))

(rf/reg-sub
 :users/active-users
 :<- [:users]
 (fn [users]
   (filter #(= (:status %) "active") users)))

(rf/reg-sub
 :users/inactive-users
 :<- [:users]
 (fn [users]
   (filter #(= (:status %) "inactive") users)))

(rf/reg-sub
 :users/promotion-seen
 (fn [db]
   (or (get-in db [:features :users :promotion-seen])
       (boolean (.getItem (.-localStorage js/window) "users-promotion-seen")))))
