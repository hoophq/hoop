(ns webapp.features.authentication.events
  (:require
   [re-frame.core :as rf]))

;; Mock data for development
(def mock-config
  {:auth-method "identity-provider"
   :selected-provider "auth0"
   :config {:client-id "your-auth0-client-id"
            :client-secret "your-auth0-client-secret"
            :custom-scopes ["email" "profile"]
            :audience "https://api.example.com"}
   :advanced {:admin-role "admin"
              :auditor-role "auditor"
              :api-key {:org-id "d9fe7aa1-b0a2-48d9-bde1"
                        :secret "VuOnc2nUwv8aCRhfQGsp"}}})

;; Get authentication configuration
(rf/reg-event-fx
 :authentication->get-config
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:authentication :status] :loading)
    :fx [[:dispatch-later {:ms 1000
                           :dispatch [:authentication->get-config-success mock-config]}]]}))

(rf/reg-event-db
 :authentication->get-config-success
 (fn [db [_ data]]
   (-> db
       (assoc-in [:authentication :status] :success)
       (assoc-in [:authentication :data] data))))

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

;; Save authentication configuration
(rf/reg-event-fx
 :authentication->save-config
 (fn [{:keys [db]} _]
   (let [config (get-in db [:authentication :data])]
     {:db (assoc-in db [:authentication :submitting?] true)
      :fx [[:dispatch [:show-snackbar {:level :info
                                       :text "Saving authentication configuration..."}]]
           ;; Mock API call - replace with actual endpoint
           [:dispatch-later {:ms 2000
                             :dispatch [:authentication->save-config-success]}]]})))

(rf/reg-event-fx
 :authentication->save-config-success
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:authentication :submitting?] false)
    :fx [[:dispatch [:show-snackbar {:level :success
                                     :text "Authentication configuration saved successfully!"}]]
         ;; Refresh configuration
         [:dispatch [:authentication->get-config]]]}))

(rf/reg-event-fx
 :authentication->save-config-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:authentication :submitting?] false)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to save authentication configuration"
                                     :details error}]]]}))

;; Generate new API key
(rf/reg-event-fx
 :authentication->generate-api-key
 (fn [{:keys [db]} _]
   {:fx [[:dispatch [:show-snackbar {:level :info
                                     :text "Generating new API key..."}]]
         ;; Mock API call - replace with actual endpoint
         [:dispatch-later {:ms 1000
                           :dispatch [:authentication->generate-api-key-success
                                      "new-generated-api-key-" (str (rand-int 1000))]}]]}))

(rf/reg-event-fx
 :authentication->generate-api-key-success
 (fn [{:keys [db]} [_ new-key]]
   {:db (assoc-in db [:authentication :data :advanced :api-key :secret] new-key)
    :fx [[:dispatch [:show-snackbar {:level :success
                                     :text "New API key generated successfully!"}]]]}))
