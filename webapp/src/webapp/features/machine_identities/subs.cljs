(ns webapp.features.machine-identities.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :machine-identities/identities
 (fn [db]
   (get-in db [:machine-identities :data])))

(rf/reg-sub
 :machine-identities/status
 (fn [db]
   (get-in db [:machine-identities :status])))

(rf/reg-sub
 :machine-identities/current-identity
 (fn [db]
   (get-in db [:machine-identities :current-identity])))

(rf/reg-sub
 :machine-identities/identity-by-id
 :<- [:machine-identities/identities]
 (fn [identities [_ id]]
   (first (filter #(= (:id %) id) identities))))
