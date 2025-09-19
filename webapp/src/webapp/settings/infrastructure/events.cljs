(ns webapp.settings.infrastructure.events
  (:require
   [clojure.string :as cs]
   [re-frame.core :as rf]))

;; Get infrastructure configuration
(rf/reg-event-fx
 :infrastructure->get-config
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:infrastructure :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/serverconfig/misc"
                             :on-success #(rf/dispatch [:infrastructure->get-config-success %])
                             :on-failure #(rf/dispatch [:infrastructure->get-config-failure %])}]]]}))

(rf/reg-event-fx
 :infrastructure->get-config-success
 (fn [{:keys [db]} [_ data]]
   (let [mapped-data {:analytics-enabled (= (:product_analytics data) "active")
                      :grpc-url (:grpc_server_url data)
                      :postgres-proxy-port (some-> (:postgres_server_config data)
                                                   :listen_address
                                                   (cs/split #":")
                                                   last)}
         updated-db (update db :infrastructure merge {:status :success :data mapped-data})]

     ;; Just update the database - no more pending connection validation needed
     {:db updated-db})))

(rf/reg-event-fx
 :infrastructure->get-config-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:infrastructure :status] :error)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to load infrastructure configuration"
                                     :details error}]]]}))

;; Update field
(rf/reg-event-db
 :infrastructure->update-field
 (fn [db [_ field value]]
   (assoc-in db [:infrastructure :data field] value)))

;; Toggle analytics
(rf/reg-event-db
 :infrastructure->toggle-analytics
 (fn [db [_ enabled?]]
   (assoc-in db [:infrastructure :data :analytics-enabled] enabled?)))

;; Save infrastructure configuration
(rf/reg-event-fx
 :infrastructure->save-config
 (fn [{:keys [db]} _]
   (let [ui-config (get-in db [:infrastructure :data])
         postgres-proxy-port (when-not (cs/blank? (:postgres-proxy-port ui-config))
                               (str "0.0.0.0:" (:postgres-proxy-port ui-config)))
         ;; Map UI structure back to API format
         api-payload {:grpc_server_url (:grpc-url ui-config)
                      :product_analytics (if (:analytics-enabled ui-config) "active" "inactive")
                      :postgres_server_config {:listen_address postgres-proxy-port}}]
     {:db (assoc-in db [:infrastructure :submitting?] true)
      :fx [[:dispatch [:fetch {:method "PUT"
                               :uri "/serverconfig/misc"
                               :body api-payload
                               :on-success #(rf/dispatch [:infrastructure->save-config-success %])
                               :on-failure #(rf/dispatch [:infrastructure->save-config-failure %])}]]]})))

(rf/reg-event-fx
 :infrastructure->save-config-success
 (fn [{:keys [db]} [_ response]]
   {:db (assoc-in db [:infrastructure :submitting?] false)
    :fx [[:dispatch [:show-snackbar {:level :success
                                     :text "Infrastructure configuration saved successfully!"}]]
         ;; Refresh configuration to ensure UI is in sync
         [:dispatch [:infrastructure->get-config]]]}))

(rf/reg-event-fx
 :infrastructure->save-config-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:infrastructure :submitting?] false)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to save infrastructure configuration"
                                     :details error}]]]}))
