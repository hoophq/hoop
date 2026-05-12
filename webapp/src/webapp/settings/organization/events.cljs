(ns webapp.settings.organization.events
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :organization-settings->get-analytics-mode
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:organization-settings :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/orgs/analytics-mode"
                             :on-success #(rf/dispatch [:organization-settings->get-analytics-mode-success %])
                             :on-failure #(rf/dispatch [:organization-settings->get-analytics-mode-failure %])}]]]}))

(rf/reg-event-fx
 :organization-settings->get-analytics-mode-success
 (fn [{:keys [db]} [_ data]]
   {:db (update db :organization-settings merge
                {:status :success
                 :analytics-mode (:analytics_mode data)})}))

(rf/reg-event-fx
 :organization-settings->get-analytics-mode-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:organization-settings :status] :error)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to load organization analytics settings"
                                     :details error}]]]}))

(rf/reg-event-db
 :organization-settings->set-analytics-mode
 (fn [db [_ mode]]
   (assoc-in db [:organization-settings :analytics-mode] mode)))

(rf/reg-event-fx
 :organization-settings->save-analytics-mode
 (fn [{:keys [db]} _]
   (let [mode (get-in db [:organization-settings :analytics-mode])]
     {:db (assoc-in db [:organization-settings :submitting?] true)
      :fx [[:dispatch [:fetch {:method "PUT"
                               :uri "/orgs/analytics-mode"
                               :body {:analytics_mode mode}
                               :on-success #(rf/dispatch [:organization-settings->save-analytics-mode-success %])
                               :on-failure #(rf/dispatch [:organization-settings->save-analytics-mode-failure %])}]]]})))

(rf/reg-event-fx
 :organization-settings->save-analytics-mode-success
 (fn [{:keys [db]} [_ response]]
   {:db (-> db
            (assoc-in [:organization-settings :submitting?] false)
            (assoc-in [:organization-settings :analytics-mode] (:analytics_mode response)))
    :fx [[:dispatch [:show-snackbar {:level :success
                                     :text "Organization settings saved"}]]
         [:dispatch [:gateway->get-info]]]}))

(rf/reg-event-fx
 :organization-settings->save-analytics-mode-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:organization-settings :submitting?] false)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to save organization settings"
                                     :details error}]]]}))
