(ns webapp.resources.views.setup.events.subs
  (:require
   [re-frame.core :as rf]))

;; Basic form state
(rf/reg-sub
 :resource-setup/current-step
 (fn [db _]
   (get-in db [:resource-setup :current-step])))

(rf/reg-sub
 :resource-setup/resource-type
 (fn [db _]
   (get-in db [:resource-setup :type])))

(rf/reg-sub
 :resource-setup/resource-subtype
 (fn [db _]
   (get-in db [:resource-setup :subtype])))

(rf/reg-sub
 :resource-setup/resource-name
 (fn [db _]
   (get-in db [:resource-setup :name])))

(rf/reg-sub
 :resource-setup/agent-id
 (fn [db _]
   (get-in db [:resource-setup :agent-id])))

;; Roles subscriptions
(rf/reg-sub
 :resource-setup/roles
 (fn [db _]
   (get-in db [:resource-setup :roles] [])))

(rf/reg-sub
 :resource-setup/role-credentials
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :credentials] {})))

;; Metadata-driven fields for custom resources
(rf/reg-sub
 :resource-setup/metadata-credentials
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :metadata-credentials] {})))

;; Environment variables and config files
(rf/reg-sub
 :resource-setup/role-env-vars
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :environment-variables] [])))

(rf/reg-sub
 :resource-setup/role-config-files
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :configuration-files] [])))

;; Created resource (for success screen)
(rf/reg-sub
 :resources->last-created
 (fn [db _]
   (get-in db [:resources :last-created])))

(rf/reg-sub
 :resources->creating?
 (fn [db _]
   (get-in db [:resources :creating?] false)))

;; From catalog
(rf/reg-sub
 :resource-setup/from-catalog?
 (fn [db _]
   (get-in db [:resource-setup :from-catalog?] false)))

(rf/reg-sub
 :resource-setup/agent-creation-mode
 (fn [db _]
   (get-in db [:resource-setup :agent-creation-mode] :select)))

