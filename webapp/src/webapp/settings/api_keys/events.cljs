(ns webapp.settings.api-keys.events
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :api-keys/list
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:api-keys :list :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/api-keys"
                             :on-success #(rf/dispatch [:api-keys/list-success %])
                             :on-failure #(rf/dispatch [:api-keys/list-failure %])}]]]}))

(rf/reg-event-db
 :api-keys/list-success
 (fn [db [_ data]]
   (update-in db [:api-keys :list] merge {:status :success :data (or data [])})))

(rf/reg-event-fx
 :api-keys/list-failure
 (fn [{:keys [db]} [_ error]]
   {:db (update-in db [:api-keys :list] merge {:status :error :error error})
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to load API keys"
                                     :details error}]]]}))

(rf/reg-event-fx
 :api-keys/get
 (fn [{:keys [db]} [_ id]]
   {:db (assoc-in db [:api-keys :active :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/api-keys/" id)
                             :on-success #(rf/dispatch [:api-keys/get-success %])
                             :on-failure #(rf/dispatch [:api-keys/get-failure %])}]]]}))

(rf/reg-event-db
 :api-keys/get-success
 (fn [db [_ data]]
   (update-in db [:api-keys :active] merge {:status :success :data data})))

(rf/reg-event-fx
 :api-keys/get-failure
 (fn [{:keys [db]} [_ error]]
   {:db (update-in db [:api-keys :active] merge {:status :error :error error})
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to load API key"
                                     :details error}]]]}))

(rf/reg-event-fx
 :api-keys/create
 (fn [{:keys [db]} [_ body]]
   {:db (assoc-in db [:api-keys :submitting?] true)
    :fx [[:dispatch [:fetch {:method "POST"
                             :uri "/api-keys"
                             :body body
                             :on-success #(rf/dispatch [:api-keys/create-success %])
                             :on-failure #(rf/dispatch [:api-keys/create-failure %])}]]]}))

(rf/reg-event-fx
 :api-keys/create-success
 (fn [{:keys [db]} [_ data]]
   {:db (-> db
            (assoc-in [:api-keys :submitting?] false)
            (assoc-in [:api-keys :created] {:status :success :data data}))
    :fx [[:dispatch [:navigate :settings-api-keys-created]]]}))

(rf/reg-event-fx
 :api-keys/create-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:api-keys :submitting?] false)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to create API key"
                                     :details error}]]]}))

(rf/reg-event-fx
 :api-keys/update
 (fn [{:keys [db]} [_ id body]]
   {:db (assoc-in db [:api-keys :submitting?] true)
    :fx [[:dispatch [:fetch {:method "PUT"
                             :uri (str "/api-keys/" id)
                             :body body
                             :on-success #(rf/dispatch [:api-keys/update-success id])
                             :on-failure #(rf/dispatch [:api-keys/update-failure %])}]]]}))

(rf/reg-event-fx
 :api-keys/update-success
 (fn [{:keys [db]} [_ id]]
   {:db (assoc-in db [:api-keys :submitting?] false)
    :fx [[:dispatch [:navigate :settings-api-keys]]
         [:dispatch [:show-snackbar {:level :success
                                     :text "API key updated successfully!"}]]]}))

(rf/reg-event-fx
 :api-keys/update-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:api-keys :submitting?] false)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to update API key"
                                     :details error}]]]}))

(rf/reg-event-fx
 :api-keys/revoke
 (fn [_ [_ id]]
   {:fx [[:dispatch [:fetch {:method "DELETE"
                             :uri (str "/api-keys/" id)
                             :on-success #(rf/dispatch [:api-keys/revoke-success])
                             :on-failure #(rf/dispatch [:api-keys/revoke-failure %])}]]]}))

(rf/reg-event-fx
 :api-keys/revoke-success
 (fn [_ _]
   {:fx [[:dispatch [:api-keys/list]]
         [:dispatch [:show-snackbar {:level :success
                                     :text "API key deactivated successfully!"}]]]}))

(rf/reg-event-fx
 :api-keys/revoke-failure
 (fn [_ [_ error]]
   {:fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to deactivate API key"
                                     :details error}]]]}))

(rf/reg-event-db
 :api-keys/clear-active
 (fn [db _]
   (assoc-in db [:api-keys :active] {:status :idle :data nil})))

(rf/reg-event-db
 :api-keys/clear-created
 (fn [db _]
   (assoc-in db [:api-keys :created] {:status :idle :data nil})))
