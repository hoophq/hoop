(ns webapp.features.access-request.events
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :access-request/list-rules
 (fn [{:keys [db]} [_]]
   {:db (assoc-in db [:access-request :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/access-requests/rules"
                             :on-success (fn [response]
                                           (rf/dispatch [:access-request/set-rules (:data response)]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:access-request/set-status :error])
                                           (rf/dispatch [:show-snackbar {:level :error
                                                                         :text "Failed to load access request rules"
                                                                         :details error}]))}]]]}))

(rf/reg-event-db
 :access-request/set-rules
 (fn [db [_ rules]]
   (-> db
       (assoc-in [:access-request :rules] rules)
       (assoc-in [:access-request :status] :ready))))

(rf/reg-event-db
 :access-request/set-status
 (fn [db [_ status]]
   (assoc-in db [:access-request :status] status)))

(rf/reg-event-fx
 :access-request/get-rule
 (fn [{:keys [db]} [_ rule-name]]
   {:db (assoc-in db [:access-request :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/access-requests/rules/" rule-name)
                             :on-success (fn [response]
                                           (rf/dispatch [:access-request/set-current-rule response]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:access-request/set-status :error])
                                           (rf/dispatch [:show-snackbar {:level :error
                                                                         :text "Failed to load access request rule"
                                                                         :details error}]))}]]]}))

(rf/reg-event-db
 :access-request/set-current-rule
 (fn [db [_ rule]]
   (-> db
       (assoc-in [:access-request :current-rule] rule)
       (assoc-in [:access-request :status] :ready))))

(rf/reg-event-db
 :access-request/clear-current-rule
 (fn [db [_]]
   (-> db
       (assoc-in [:access-request :current-rule] nil)
       (assoc-in [:access-request :status] :idle))))

(rf/reg-event-fx
 :access-request/create-rule
 (fn [{:keys [db]} [_ rule-data]]
   {:db db
    :fx [[:dispatch [:fetch {:method "POST"
                             :uri "/access-requests/rules"
                             :body rule-data
                             :on-success #(rf/dispatch [:access-request/create-success rule-data])
                             :on-failure #(rf/dispatch [:access-request/create-failure %])}]]]}))

(rf/reg-event-fx
 :access-request/create-success
 (fn [{:keys [db]} [_ rule-data]]
   {:db db
    :fx [[:dispatch [:navigate :access-request]]
         [:dispatch [:show-snackbar {:level :success
                                     :text (str "Access Request rule '" (:name rule-data) "' created successfully!")}]]
         [:dispatch [:access-request/list-rules]]]}))

(rf/reg-event-fx
 :access-request/create-failure
 (fn [{:keys [db]} [_ error]]
   {:db (update-in db [:access-request :list] merge {:status :error :error error})
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to create access request rule"
                                     :details error}]]]}))

(rf/reg-event-fx
 :access-request/update-rule
 (fn [{:keys [db]} [_ rule-name rule-data]]
   {:db db
    :fx [[:dispatch [:fetch {:method "PUT"
                             :uri (str "/access-requests/rules/" rule-name)
                             :body rule-data
                             :on-success #(rf/dispatch [:access-request/update-success rule-name])
                             :on-failure #(rf/dispatch [:access-request/update-failure %])}]]]}))

(rf/reg-event-fx
 :access-request/update-success
 (fn [{:keys [db]} [_ rule-name]]
   {:db db
    :fx [[:dispatch [:navigate :access-request]]
         [:dispatch [:show-snackbar {:level :success
                                     :text (str "Access Request rule '" rule-name "' updated successfully!")}]]
         [:dispatch [:access-request/list-rules]]]}))

(rf/reg-event-fx
 :access-request/update-failure
 (fn [{:keys [db]} [_ error]]
   {:db (update-in db [:access-request :list] merge {:status :error :error error})
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to update access request rule"
                                     :details error}]]]}))

(rf/reg-event-fx
 :access-request/delete-rule
 (fn [{:keys [db]} [_ rule-name]]
   {:db db
    :fx [[:dispatch [:fetch {:method "DELETE"
                             :uri (str "/access-requests/rules/" rule-name)
                             :on-success #(rf/dispatch [:access-request/delete-success rule-name])
                             :on-failure #(rf/dispatch [:access-request/delete-failure %])}]]]}))

(rf/reg-event-fx
 :access-request/delete-success
 (fn [{:keys [db]} [_ rule-name]]
   {:db db
    :fx [[:dispatch [:navigate :access-request]]
         [:dispatch [:show-snackbar {:level :success
                                     :text (str "Access Request rule '" rule-name "' deleted successfully!")}]]
         [:dispatch [:access-request/list-rules]]]}))

(rf/reg-event-fx
 :access-request/delete-failure
 (fn [{:keys [db]} [_ error]]
   {:db (update-in db [:access-request :list] merge {:status :error :error error})
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to delete access request rule"
                                     :details error}]]]}))
