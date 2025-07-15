(ns webapp.settings.infrastructure.events
  (:require
   [re-frame.core :as rf]))

;; Mock data for development
(def mock-config
  {:analytics-enabled true
   :grpc-url "grpcdemo.v1.countryservice"})

;; Get infrastructure configuration
(rf/reg-event-fx
 :infrastructure->get-config
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:infrastructure :status] :loading)
    :fx [[:dispatch-later {:ms 1000
                           :dispatch [:infrastructure->get-config-success mock-config]}]]}))

(rf/reg-event-db
 :infrastructure->get-config-success
 (fn [db [_ data]]
   (-> db
       (assoc-in [:infrastructure :status] :success)
       (assoc-in [:infrastructure :data] data))))

(rf/reg-event-db
 :infrastructure->get-config-failure
 (fn [db [_ error]]
   (-> db
       (assoc-in [:infrastructure :status] :error)
       (assoc-in [:infrastructure :error] error))))

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
   (let [config (get-in db [:infrastructure :data])]
     {:db (assoc-in db [:infrastructure :submitting?] true)
      :fx [[:dispatch [:show-snackbar {:level :info
                                       :text "Saving infrastructure configuration..."}]]
           ;; Mock API call - replace with actual endpoint
           ;; POST/PUT /api/infrastructure/config
           [:dispatch-later {:ms 2000
                             :dispatch [:infrastructure->save-config-success]}]]})))

(rf/reg-event-fx
 :infrastructure->save-config-success
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:infrastructure :submitting?] false)
    :fx [[:dispatch [:show-snackbar {:level :success
                                     :text "Infrastructure configuration saved successfully!"}]]
         ;; Refresh configuration
         [:dispatch [:infrastructure->get-config]]]}))

(rf/reg-event-fx
 :infrastructure->save-config-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:infrastructure :submitting?] false)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to save infrastructure configuration"
                                     :details error}]]]}))
