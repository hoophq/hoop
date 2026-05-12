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
                             :on-failure #(rf/dispatch [:infrastructure->get-config-failure %])}]]
         [:dispatch [:infrastructure->get-analytics-mode]]]}))

(rf/reg-event-fx
 :infrastructure->get-config-success
 (fn [{:keys [db]} [_ data]]
   (let [mapped-data {:grpc-url (:grpc_server_url data)
                      :postgres-proxy-port (some-> (:postgres_server_config data)
                                                   :listen_address
                                                   (cs/split #":")
                                                   last)
                      :ssh-proxy-port (some-> (:ssh_server_config data)
                                              :listen_address
                                              (cs/split #":")
                                              last)
                      :rdp-proxy-port (some-> (:rdp_server_config data)
                                              :listen_address
                                              (cs/split #":")
                                              last)
                      :http-proxy-port (some-> (:http_proxy_server_config data)
                                               :listen_address
                                               (cs/split #":")
                                               last)}

         updated-db (-> db
                        (assoc-in [:infrastructure :status] :success)
                        (update-in [:infrastructure :data] merge mapped-data))]

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

;; Save infrastructure configuration
(rf/reg-event-fx
 :infrastructure->save-config
 (fn [{:keys [db]} _]
   (let [ui-config (get-in db [:infrastructure :data])
         postgres-proxy-port (when-not (cs/blank? (:postgres-proxy-port ui-config))
                               (str "0.0.0.0:" (:postgres-proxy-port ui-config)))
         ssh-proxy-port (when-not (cs/blank? (:ssh-proxy-port ui-config))
                          (str "0.0.0.0:" (:ssh-proxy-port ui-config)))
         rdp-proxy-port (when-not (cs/blank? (:rdp-proxy-port ui-config))
                          (str "0.0.0.0:" (:rdp-proxy-port ui-config)))

         http-proxy-port (when-not (cs/blank? (:http-proxy-port ui-config))
                           (str "0.0.0.0:" (:http-proxy-port ui-config)))
         ;; Map UI structure back to API format
         api-payload {:grpc_server_url (:grpc-url ui-config)
                      :postgres_server_config {:listen_address postgres-proxy-port}
                      :ssh_server_config {:listen_address ssh-proxy-port}
                      :rdp_server_config {:listen_address rdp-proxy-port}
                      :http_proxy_server_config {:listen_address http-proxy-port}}]
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

;; Analytics mode
(rf/reg-event-fx
 :infrastructure->get-analytics-mode
 (fn [_ _]
   {:fx [[:dispatch [:fetch {:method "GET"
                             :uri "/orgs/analytics-mode"
                             :on-success #(rf/dispatch [:infrastructure->get-analytics-mode-success %])
                             :on-failure #(rf/dispatch [:infrastructure->get-analytics-mode-failure %])}]]]}))

(rf/reg-event-db
 :infrastructure->get-analytics-mode-success
 (fn [db [_ data]]
   (assoc-in db [:infrastructure :data :analytics-mode] (:analytics_mode data))))

(rf/reg-event-fx
 :infrastructure->get-analytics-mode-failure
 (fn [_ [_ error]]
   {:fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to load analytics privacy settings"
                                     :details error}]]]}))

(rf/reg-event-fx
 :infrastructure->set-analytics-mode
 (fn [{:keys [db]} [_ new-mode]]
   (let [previous-mode (get-in db [:infrastructure :data :analytics-mode])]
     (if (or (= previous-mode new-mode)
             (get-in db [:infrastructure :analytics-saving?]))
       {:db db}
       {:db (-> db
                (assoc-in [:infrastructure :data :analytics-mode] new-mode)
                (assoc-in [:infrastructure :analytics-saving?] true))
        :fx [[:dispatch [:fetch {:method "PUT"
                                 :uri "/orgs/analytics-mode"
                                 :body {:analytics_mode new-mode}
                                 :on-success #(rf/dispatch [:infrastructure->set-analytics-mode-success %])
                                 :on-failure #(rf/dispatch [:infrastructure->set-analytics-mode-failure previous-mode %])}]]]}))))

(rf/reg-event-fx
 :infrastructure->set-analytics-mode-success
 (fn [{:keys [db]} [_ response]]
   {:db (-> db
            (assoc-in [:infrastructure :data :analytics-mode] (:analytics_mode response))
            (assoc-in [:infrastructure :analytics-saving?] false))
    :fx [[:dispatch [:show-snackbar {:level :success
                                     :text "Analytics privacy updated"}]]
         [:dispatch [:gateway->get-info]]]}))

(rf/reg-event-fx
 :infrastructure->set-analytics-mode-failure
 (fn [{:keys [db]} [_ previous-mode error]]
   {:db (-> db
            (assoc-in [:infrastructure :data :analytics-mode] previous-mode)
            (assoc-in [:infrastructure :analytics-saving?] false))
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to update analytics privacy"
                                     :details error}]]]}))
