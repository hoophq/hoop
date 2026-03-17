(ns webapp.features.ai-session-analyzer.events
  (:require
   [re-frame.core :as rf]))

;; Provider Events

(rf/reg-event-fx
 :ai-session-analyzer/get-provider
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:ai-session-analyzer :provider :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/ai/session-analyzer/providers"
                             :on-success #(rf/dispatch [:ai-session-analyzer/get-provider-success %])
                             :on-failure #(rf/dispatch [:ai-session-analyzer/get-provider-failure %])}]]]}))

(rf/reg-event-db
 :ai-session-analyzer/get-provider-success
 (fn [db [_ data]]
   (update-in db [:ai-session-analyzer :provider] merge {:status :success :data data})))

(rf/reg-event-fx
 :ai-session-analyzer/get-provider-failure
 (fn [{:keys [db]} [_ error]]
   (let [status-code (or (:status error)
                         (:status-code error)
                         (get-in error [:response :status])
                         (get-in error [:response :status-code]))]
     (if (= status-code 404) 
       {:db (assoc-in db [:ai-session-analyzer :provider] {:status :idle :data nil :error nil})}
       (let [error-message (or (:message error) (str error))]
         {:db (update-in db [:ai-session-analyzer :provider] merge {:status :error :error error})
          :fx [[:dispatch [:show-snackbar
                           {:level :error
                            :text "Failed to load AI provider configuration"
                            :details error-message}]]]})))))

(rf/reg-event-fx
 :ai-session-analyzer/upsert-provider
 (fn [{:keys [db]} [_ provider-data on-success on-failure]]
   {:db (assoc-in db [:ai-session-analyzer :provider :status] :loading)
    :fx [[:dispatch [:fetch {:method "POST"
                             :uri "/ai/session-analyzer/providers"
                             :body provider-data
                             :on-success #(do
                                            (rf/dispatch [:ai-session-analyzer/upsert-provider-success %])
                                            (when on-success (on-success)))
                             :on-failure #(do
                                            (rf/dispatch [:ai-session-analyzer/upsert-provider-failure %])
                                            (when on-failure (on-failure %)))}]]]}))

(rf/reg-event-db
 :ai-session-analyzer/upsert-provider-success
 (fn [db [_ data]]
   (update-in db [:ai-session-analyzer :provider] merge {:status :success :data data})))

(rf/reg-event-fx
 :ai-session-analyzer/upsert-provider-failure
 (fn [{:keys [db]} [_ error]]
   (let [error-message (or (:message error) (str error))]
     {:db (update-in db [:ai-session-analyzer :provider] merge {:status :error :error error})
      :fx [[:dispatch [:show-snackbar
                       {:level :error
                        :text "Failed to save provider configuration"
                        :details error-message}]]]})))

(rf/reg-event-fx
 :ai-session-analyzer/delete-provider
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:ai-session-analyzer :provider :status] :loading)
    :fx [[:dispatch [:fetch {:method "DELETE"
                             :uri "/ai/session-analyzer/providers"
                             :on-success #(rf/dispatch [:ai-session-analyzer/delete-provider-success])
                             :on-failure #(rf/dispatch [:ai-session-analyzer/delete-provider-failure %])}]]]}))

(rf/reg-event-db
 :ai-session-analyzer/delete-provider-success
 (fn [db _]
   (assoc-in db [:ai-session-analyzer :provider] {:status :idle :data nil :error nil})))

(rf/reg-event-fx
 :ai-session-analyzer/delete-provider-failure
 (fn [{:keys [db]} [_ error]]
   (let [error-message (or (:message error) (str error))]
     {:db (update-in db [:ai-session-analyzer :provider] merge {:status :error :error error})
      :fx [[:dispatch [:show-snackbar
                       {:level :error
                        :text "Failed to delete provider configuration"
                        :details error-message}]]]})))

;; Rules Events

(rf/reg-event-fx
 :ai-session-analyzer/get-rules
 (fn [{:keys [db]} [_ {:keys [connection-names]}]]
   (let [query-params (cond-> {}
                        (seq connection-names) (assoc :connection_names connection-names))
         status (get-in db [:ai-session-analyzer :rules :status])]
     {:db (if (= status :success)
            db
            (assoc-in db [:ai-session-analyzer :rules :status] :loading))
      :fx [[:dispatch [:fetch {:method "GET"
                               :uri "/ai/session-analyzer/rules"
                               :query-params query-params
                               :on-success #(rf/dispatch [:ai-session-analyzer/get-rules-success %])
                               :on-failure #(rf/dispatch [:ai-session-analyzer/get-rules-failure %])}]]]})))

(rf/reg-event-db
 :ai-session-analyzer/get-rules-success
 (fn [db [_ data]]
   (let [rules (or (:data data) [])]
     (update-in db [:ai-session-analyzer :rules] merge {:status :success :data rules}))))

(rf/reg-event-fx
 :ai-session-analyzer/get-rules-failure
 (fn [{:keys [db]} [_ error]]
   (let [error-message (or (:message error) (str error))]
     {:db (update-in db [:ai-session-analyzer :rules] merge {:status :error :error error})
      :fx [[:dispatch [:show-snackbar
                       {:level :error
                        :text "Failed to load AI Session Analyzer rules"
                        :details error-message}]]]})))

(rf/reg-event-fx
 :ai-session-analyzer/get-rule-by-name
 (fn [{:keys [db]} [_ rule-name]]
   {:db (assoc-in db [:ai-session-analyzer :active-rule :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/ai/session-analyzer/rules/" rule-name)
                             :on-success #(rf/dispatch [:ai-session-analyzer/get-rule-by-name-success %])
                             :on-failure #(rf/dispatch [:ai-session-analyzer/get-rule-by-name-failure %])}]]]}))

(rf/reg-event-db
 :ai-session-analyzer/get-rule-by-name-success
 (fn [db [_ data]]
   (update-in db [:ai-session-analyzer :active-rule] merge {:status :success :data data})))

(rf/reg-event-fx
 :ai-session-analyzer/get-rule-by-name-failure
 (fn [{:keys [db]} [_ error]]
   (let [error-message (or (:message error) (str error))]
     {:db (update-in db [:ai-session-analyzer :active-rule] merge {:status :error :error error})
      :fx [[:dispatch [:show-snackbar
                       {:level :error
                        :text "Failed to load rule"
                        :details error-message}]]]})))

(rf/reg-event-fx
 :ai-session-analyzer/create-rule
 (fn [{:keys [db]} [_ rule-data]]
   {:db (assoc-in db [:ai-session-analyzer :rule :status] :loading)
    :fx [[:dispatch [:fetch {:method "POST"
                             :uri "/ai/session-analyzer/rules"
                             :body rule-data
                             :on-success #(rf/dispatch [:ai-session-analyzer/create-rule-success %])
                             :on-failure #(rf/dispatch [:ai-session-analyzer/create-rule-failure %])}]]]}))

(rf/reg-event-fx
 :ai-session-analyzer/create-rule-success
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:ai-session-analyzer :rule :status] :success)
    :fx [[:dispatch [:navigate :ai-session-analyzer]]
         [:dispatch [:show-snackbar
                     {:level :success
                      :text "Rule created successfully!"}]]]}))

(rf/reg-event-fx
 :ai-session-analyzer/create-rule-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:ai-session-analyzer :rule :status] :error)
    :fx [[:dispatch [:show-snackbar
                     {:level :error
                      :text "Failed to create rule"
                      :details error}]]]}))

(rf/reg-event-fx
 :ai-session-analyzer/update-rule
 (fn [{:keys [db]} [_ rule-name rule-data]]
   {:db (assoc-in db [:ai-session-analyzer :rule :status] :loading)
    :fx [[:dispatch [:fetch {:method "PUT"
                             :uri (str "/ai/session-analyzer/rules/" rule-name)
                             :body rule-data
                             :on-success #(rf/dispatch [:ai-session-analyzer/update-rule-success %])
                             :on-failure #(rf/dispatch [:ai-session-analyzer/update-rule-failure %])}]]]}))

(rf/reg-event-fx
 :ai-session-analyzer/update-rule-success
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:ai-session-analyzer :rule :status] :success)
    :fx [[:dispatch [:navigate :ai-session-analyzer]]
         [:dispatch [:show-snackbar
                     {:level :success
                      :text "Rule updated successfully!"}]]]}))

(rf/reg-event-fx
 :ai-session-analyzer/update-rule-failure
 (fn [{:keys [db]} [_ error]]
   (let [error-message (or (:message error) (str error))]
     {:db (assoc-in db [:ai-session-analyzer :rule :status] :error)
      :fx [[:dispatch [:show-snackbar
                       {:level :error
                        :text "Failed to update rule"
                        :details error-message}]]]})))

(rf/reg-event-db
 :ai-session-analyzer/clear-active-rule
 (fn [db _]
   (assoc-in db [:ai-session-analyzer :active-rule] {:status :idle :data nil :error nil})))

(rf/reg-event-fx
 :ai-session-analyzer/delete-rule
 (fn [{:keys [db]} [_ rule-name]]
   {:db db
    :fx [[:dispatch [:fetch {:method "DELETE"
                             :uri (str "/ai/session-analyzer/rules/" rule-name)
                             :on-success #(rf/dispatch [:ai-session-analyzer/delete-rule-success rule-name])
                             :on-failure #(rf/dispatch [:ai-session-analyzer/delete-rule-failure %])}]]]}))

(rf/reg-event-fx
 :ai-session-analyzer/delete-rule-success
 (fn [{:keys [db]} _]
   {:db db
    :fx [[:dispatch [:navigate :ai-session-analyzer]]
         [:dispatch [:show-snackbar
                     {:level :success
                      :text "Rule deleted successfully!"}]]]}))

(rf/reg-event-fx
 :ai-session-analyzer/delete-rule-failure
 (fn [{:keys [db]} [_ error]]
   (let [error-message (or (:message error) (str error))]
     {:db db
      :fx [[:dispatch [:show-snackbar
                       {:level :error
                        :text "Failed to delete rule"
                        :details error-message}]]]})))
