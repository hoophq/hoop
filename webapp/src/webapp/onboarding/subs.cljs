(ns webapp.onboarding.subs
  (:require [re-frame.core :as rf]))

;; Basic AWS Connect subscriptions
(rf/reg-sub
 :aws-connect/credentials
 (fn [db _]
   (get-in db [:aws-connect :credentials])))

(rf/reg-sub
 :aws-connect/status
 (fn [db _]
   (get-in db [:aws-connect :status])))

(rf/reg-sub
 :aws-connect/error
 (fn [db _]
   (get-in db [:aws-connect :error])))

(rf/reg-sub
 :aws-connect/current-step
 (fn [db _]
   (get-in db [:aws-connect :current-step])))

;; Accounts related subscriptions
(rf/reg-sub
 :aws-connect/accounts
 (fn [db _]
   (get-in db [:aws-connect :accounts :data])))

(rf/reg-sub
 :aws-connect/selected-accounts
 (fn [db _]
   (get-in db [:aws-connect :accounts :selected])))

;; Resources related subscriptions
(rf/reg-sub
 :aws-connect/resources
 (fn [db _]
   (get-in db [:aws-connect :resources :data])))

(rf/reg-sub
 :aws-connect/selected-resources
 (fn [db _]
   (get-in db [:aws-connect :resources :selected])))

(rf/reg-sub
 :aws-connect/resources-status
 (fn [db _]
   (get-in db [:aws-connect :resources :status])))

(rf/reg-sub
 :aws-connect/resources-errors
 (fn [db _]
   (get-in db [:aws-connect :resources :errors])))

;; Agents related subscriptions
(rf/reg-sub
 :aws-connect/agents
 (fn [db _]
   (get-in db [:aws-connect :agents :data])))

(rf/reg-sub
 :aws-connect/agent-assignments
 (fn [db _]
   (get-in db [:aws-connect :agents :assignments])))

;; Connection setup type subscription
(rf/reg-sub
 :connection-setup/type
 (fn [db _]
   (get-in db [:connection-setup :type])))
