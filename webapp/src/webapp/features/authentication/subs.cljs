(ns webapp.features.authentication.subs
  (:require
   [re-frame.core :as rf]))

;; Main authentication config subscription
(rf/reg-sub
 :authentication->config
 (fn [db _]
   (get db :authentication)))

;; Authentication data
(rf/reg-sub
 :authentication->data
 :<- [:authentication->config]
 (fn [config _]
   (:data config)))

;; Current authentication method
(rf/reg-sub
 :authentication->auth-method
 :<- [:authentication->data]
 (fn [data _]
   (:auth-method data)))

;; Selected identity provider
(rf/reg-sub
 :authentication->selected-provider
 :<- [:authentication->data]
 (fn [data _]
   (:selected-provider data)))

;; Provider configuration
(rf/reg-sub
 :authentication->provider-config
 :<- [:authentication->data]
 (fn [data _]
   (:config data)))

;; Advanced configuration
(rf/reg-sub
 :authentication->advanced-config
 :<- [:authentication->data]
 (fn [data _]
   (:advanced data)))

;; API Key configuration
(rf/reg-sub
 :authentication->api-key
 :<- [:authentication->advanced-config]
 (fn [advanced _]
   (:api-key advanced)))

;; Submitting state
(rf/reg-sub
 :authentication->submitting?
 :<- [:authentication->config]
 (fn [config _]
   (:submitting? config)))
