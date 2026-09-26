;; The AI Session Analyzer pages live in React (webapp_v2
;; pages/Features/AiSessionAnalyzer). What survives here is the read path the
;; webclient, the runbooks runner and the activation journey still dispatch.
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
 :ai-session-analyzer/get-role-rule
 (fn [{:keys [db]} [_ role-name-or-id]]
   (if (some? role-name-or-id)
     {:db (assoc-in db [:ai-session-analyzer :role-rule] {:status :loading :data nil :error nil})
      :fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/connections/" role-name-or-id "/ai-session-analyzer-rule")
                               :on-success #(rf/dispatch [:ai-session-analyzer/get-role-rule-success %])
                               :on-failure #(rf/dispatch [:ai-session-analyzer/get-role-rule-failure %])}]]]}
     {:db (assoc-in db [:ai-session-analyzer :role-rule] {:status :idle :data nil :error nil})})))

(rf/reg-event-db
 :ai-session-analyzer/get-role-rule-success
 (fn [db [_ data]]
   (assoc-in db [:ai-session-analyzer :role-rule] {:status :success :data data :error nil})))

(rf/reg-event-fx
 :ai-session-analyzer/get-role-rule-failure
 (fn [{:keys [db]} [_ error]]
   (let [status-code (or (:status error)
                         (:status-code error)
                         (get-in error [:response :status])
                         (get-in error [:response :status-code]))]
     (if (= status-code 404)
       {:db (assoc-in db [:ai-session-analyzer :role-rule] {:status :idle :data nil :error nil})}
       (let [error-message (or (:message error) (str error))]
         {:db (assoc-in db [:ai-session-analyzer :role-rule] {:status :error :data nil :error error})
          :fx [[:dispatch [:show-snackbar
                           {:level :error
                            :text "Failed to load AI Session Analyzer role rule"
                            :details error-message}]]]})))))

(rf/reg-event-db
 :ai-session-analyzer/clear-role-rule
 (fn [db _]
   (assoc-in db [:ai-session-analyzer :role-rule] {:status :idle :data nil :error nil})))

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
