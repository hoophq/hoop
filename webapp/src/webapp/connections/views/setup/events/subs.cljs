(ns webapp.connections.views.setup.events.subs
  (:require [re-frame.core :as rf]))

;; Basic form state
(rf/reg-sub
 :connection-setup/current-step
 (fn [db _]
   (get-in db [:connection-setup :current-step])))

(rf/reg-sub
 :connection-setup/connection-type
 (fn [db _]
   (get-in db [:connection-setup :type])))

(rf/reg-sub
 :connection-setup/connection-subtype
 (fn [db _]
   (get-in db [:connection-setup :subtype])))

(rf/reg-sub
 :connection-setup/name
 (fn [db]
   (get-in db [:connection-setup :name])))

;; App type and OS
(rf/reg-sub
 :connection-setup/app-type
 (fn [db _]
   (get-in db [:connection-setup :app-type])))

(rf/reg-sub
 :connection-setup/os-type
 (fn [db _]
   (get-in db [:connection-setup :os-type])))

(rf/reg-sub
 :connection-setup/database-credentials
 (fn [db _]
   (get-in db [:connection-setup :database-credentials])))

(rf/reg-sub
 :connection-setup/command
 (fn [db]
   (get-in db [:connection-setup :command] "")))

;; Configuration and features
(rf/reg-sub
 :connection-setup/config
 (fn [db]
   (get-in db [:connection-setup :config])))

(rf/reg-sub
 :connection-setup/review
 :<- [:connection-setup/config]
 (fn [config]
   (:review config false)))

(rf/reg-sub
 :connection-setup/data-masking
 :<- [:connection-setup/config]
 (fn [config]
   (:data-masking config false)))

(rf/reg-sub
 :connection-setup/database-schema
 :<- [:connection-setup/config]
 (fn [config]
   (:database-schema config true)))

(rf/reg-sub
 :connection-setup/access-modes
 :<- [:connection-setup/config]
 (fn [config]
   (get-in config [:access-modes] {:runbooks true, :native true, :web true})))

;; Review and masking types
(rf/reg-sub
 :connection-setup/review-groups
 :<- [:connection-setup/config]
 (fn [config]
   (get-in config [:review-groups] [])))

(rf/reg-sub
 :connection-setup/data-masking-types
 :<- [:connection-setup/config]
 (fn [config]
   (get-in config [:data-masking-types] [])))

;; Network specific
(rf/reg-sub
 :connection-setup/network-type
 (fn [db]
   (get-in db [:connection-setup :network-type])))

(rf/reg-sub
 :connection-setup/network-credentials
 (fn [db]
   (get-in db [:connection-setup :network-credentials] {})))

;; Agent
(rf/reg-sub
 :connection-setup/agent-id
 (fn [db]
   (get-in db [:connection-setup :agent-id])))

;; Jira template ID
(rf/reg-sub
 :connection-setup/jira-template-id
 (fn [db]
   (get-in db [:connection-setup :config :jira-template-id])))

;; Guardrails
(rf/reg-sub
 :connection-setup/guardrails
 (fn [db]
   (get-in db [:connection-setup :config :guardrails] [])))

;; Environment Variables
(rf/reg-sub
 :connection-setup/env-current-key
 (fn [db]
   (get-in db [:connection-setup :credentials :current-key])))

(rf/reg-sub
 :connection-setup/env-current-value
 (fn [db]
   (get-in db [:connection-setup :credentials :current-value])))

(rf/reg-sub
 :connection-setup/environment-variables
 (fn [db]
   (get-in db [:connection-setup :credentials :environment-variables])))

;; Configuration Files subscriptions
(rf/reg-sub
 :connection-setup/config-current-name
 (fn [db]
   (get-in db [:connection-setup :credentials :current-file-name])))

(rf/reg-sub
 :connection-setup/config-current-content
 (fn [db]
   (get-in db [:connection-setup :credentials :current-file-content])))

(rf/reg-sub
 :connection-setup/configuration-files
 (fn [db]
   (get-in db [:connection-setup :credentials :configuration-files])))

;; Sub para tags
(rf/reg-sub
 :connection-setup/tags
 (fn [db]
   (get-in db [:connection-setup :tags] [])))

(rf/reg-sub
 :connection-setup/tags-input
 (fn [db]
   (get-in db [:connection-setup :tags-input] [])))

;; Subscriptions específicos para atualização
(rf/reg-sub
 :connection-setup/form-data
 (fn [db]
   (:connection-setup db)))
