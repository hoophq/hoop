(ns webapp.integrations.authentication.events
  (:require
   [re-frame.core :as rf]))

;; Get authentication configuration
(rf/reg-event-fx
 :authentication->get-config
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:authentication :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/serverconfig/auth"
                             :on-success #(rf/dispatch [:authentication->get-config-success %])
                             :on-failure #(rf/dispatch [:authentication->get-config-failure %])}]]]}))

(rf/reg-event-db
 :authentication->get-config-success
 (fn [db [_ data]]
   (let [mapped-data (-> data
                         ;; Map API fields to UI structure
                         (assoc :auth-method (if (= (:auth_method data) "oidc") "identity-provider" "local"))
                         (assoc :selected-provider (when (= (:auth_method data) "oidc") "auth0"))
                         (assoc :config (merge
                                         {:client-id (:client_id (:oidc_config data))
                                          :client-secret (:client_secret (:oidc_config data))
                                          :custom-scopes (:scopes (:oidc_config data))
                                          :audience (:audience (:oidc_config data))}))
                         (assoc :advanced {:admin-role (:admin_role_name data)
                                           :auditor-role (:auditor_role_name data)
                                           :api-key {:org-id nil ;; Not used in new API
                                                     :secret (:rollout_api_key data)}
                                           :local-auth-enabled (= (:webapp_users_management_status data) "enabled")}))]
     (-> db
         (assoc-in [:authentication :status] :success)
         (assoc-in [:authentication :data] mapped-data)))))

(rf/reg-event-fx
 :authentication->get-config-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:authentication :status] :error)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to load authentication configuration"
                                     :details error}]]]}))

;; Update authentication method
(rf/reg-event-db
 :authentication->set-auth-method
 (fn [db [_ method]]
   (assoc-in db [:authentication :data :auth-method] method)))

;; Update selected provider
(rf/reg-event-db
 :authentication->set-provider
 (fn [db [_ provider]]
   (-> db
       (assoc-in [:authentication :data :selected-provider] provider)
       ;; Reset config when changing provider
       (assoc-in [:authentication :data :config] {:custom-scopes ["email" "profile"]}))))

;; Update provider configuration field
(rf/reg-event-db
 :authentication->update-config-field
 (fn [db [_ field value]]
   (assoc-in db [:authentication :data :config field] value)))

;; Update advanced configuration field
(rf/reg-event-db
 :authentication->update-advanced-field
 (fn [db [_ field value]]
   (assoc-in db [:authentication :data :advanced field] value)))

;; Toggle local authentication
(rf/reg-event-db
 :authentication->toggle-local-auth
 (fn [db [_ enabled?]]
   (assoc-in db [:authentication :data :advanced :local-auth-enabled] enabled?)))

;; Generate new API key
(rf/reg-event-fx
 :authentication->generate-api-key
 (fn [{:keys [db]} _]
   {:fx [[:dispatch [:fetch {:method "POST"
                             :uri "/serverconfig/auth/apikey"
                             :on-success #(rf/dispatch [:authentication->generate-api-key-success %])
                             :on-failure #(rf/dispatch [:authentication->generate-api-key-failure %])}]]]}))

(rf/reg-event-fx
 :authentication->generate-api-key-success
 (fn [{:keys [db]} [_ response]]
   {:db (assoc-in db [:authentication :data :advanced :api-key :secret] (:rollout_api_key response))
    :fx [[:dispatch [:show-snackbar {:level :success
                                     :text "New API key generated successfully!"}]]]}))

(rf/reg-event-fx
 :authentication->generate-api-key-failure
 (fn [{:keys [db]} [_ error]]
   {:fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to generate new API key"
                                     :details error}]]]}))

;; Save authentication configuration
(rf/reg-event-fx
 :authentication->save-config
 (fn [{:keys [db]} _]
   (let [ui-config (get-in db [:authentication :data])
         ;; Map UI structure back to API format
         api-payload {:auth_method (if (= (:auth-method ui-config) "identity-provider") "oidc" "local")
                      :admin_role_name (get-in ui-config [:advanced :admin-role])
                      :auditor_role_name (get-in ui-config [:advanced :auditor-role])
                      :rollout_api_key (get-in ui-config [:advanced :api-key :secret])
                      :webapp_users_management_status (if (get-in ui-config [:advanced :local-auth-enabled]) "active" "inactive")
                      :oidc_config (when (= (:auth-method ui-config) "identity-provider")
                                     {:client_id (get-in ui-config [:config :client-id])
                                      :client_secret (get-in ui-config [:config :client-secret])
                                      :audience (get-in ui-config [:config :audience])
                                      :groups_claim "groups"
                                      :issuer_url ""  ;; TODO: Add issuer URL field to UI
                                      :scopes (or (get-in ui-config [:config :custom-scopes]) ["openid" "email" "profile"])})
                      :saml_config nil}]
     {:db (assoc-in db [:authentication :submitting?] true)
      :fx [[:dispatch [:fetch {:method "PUT"
                               :uri "/serverconfig/auth"
                               :body api-payload
                               :on-success #(rf/dispatch [:authentication->save-config-success %])
                               :on-failure #(rf/dispatch [:authentication->save-config-failure %])}]]]})))

(rf/reg-event-fx
 :authentication->save-config-success
 (fn [{:keys [db]} [_ response]]
   {:db (assoc-in db [:authentication :submitting?] false)
    :fx [[:dispatch [:show-snackbar {:level :success
                                     :text "Authentication configuration saved successfully!"}]]
         ;; Refresh configuration to ensure UI is in sync
         [:dispatch [:authentication->get-config]]]}))

(rf/reg-event-fx
 :authentication->save-config-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:authentication :submitting?] false)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to save authentication configuration"
                                     :details error}]]]}))

