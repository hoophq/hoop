(ns webapp.settings.infrastructure.subs
  (:require
   [re-frame.core :as rf]))

;; Main infrastructure config subscription
(rf/reg-sub
 :infrastructure->config
 (fn [db _]
   (get db :infrastructure)))

;; Infrastructure data
(rf/reg-sub
 :infrastructure->data
 :<- [:infrastructure->config]
 (fn [config _]
   (:data config)))

;; Analytics enabled state
(rf/reg-sub
 :infrastructure->analytics-enabled?
 :<- [:infrastructure->data]
 (fn [data _]
   (:analytics-enabled data)))

;; gRPC URL
(rf/reg-sub
 :infrastructure->grpc-url
 :<- [:infrastructure->data]
 (fn [data _]
   (:grpc-url data)))

;; Submitting state
(rf/reg-sub
 :infrastructure->submitting?
 :<- [:infrastructure->config]
 (fn [config _]
   (:submitting? config)))
