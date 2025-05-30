(ns webapp.events.jira-templates
  (:require
   [re-frame.core :as rf]
   [webapp.jira-templates.prompt-form :as prompt-form]
   [webapp.jira-templates.loading-jira-templates :as loading-jira-templates]
   [webapp.jira-templates.cmdb-error :as cmdb-error]))

;; CMDB

(rf/reg-event-fx
 :jira-templates->get-cmdb-values
 (fn [{:keys [db]} [_ template-id cmdb-item]]
   {:fx [[:dispatch
          [:fetch {:method "GET"
                   :uri (str "/integrations/jira/issuetemplates/"
                             template-id
                             "/objecttype-values?object_type="
                             (:jira_object_type cmdb-item))
                   :on-success #(rf/dispatch [:jira-templates->merge-cmdb-values cmdb-item %])
                   :on-failure #(rf/dispatch [:jira-templates->merge-cmdb-values cmdb-item nil])}]]]}))

(rf/reg-event-fx
 :jira-templates->merge-cmdb-values
 (fn [{:keys [db]} [_ cmdb-item value]]
   (let [current-template (get-in db [:jira-templates->submit-template :data])
         cmdb-items (get-in current-template [:cmdb_types :items])
         ;; Track if this was a failed request
         failed-request? (nil? value)
         ;; Update items
         updated-cmdb-items (map (fn [item]
                                   (if (= (:jira_object_type item) (:jira_object_type cmdb-item))
                                     (-> item
                                         (merge value)
                                         (assoc :request-failed failed-request?))
                                     item))
                                 cmdb-items)
         updated-template (assoc-in current-template [:cmdb_types :items] updated-cmdb-items)
         ;; Check if all requests completed (success or failure)
         all-requests-completed? (every? #(or (contains? % :jira_values)
                                              (:request-failed %))
                                         updated-cmdb-items)
         ;; Check if any requests failed
         any-requests-failed? (some :request-failed updated-cmdb-items)
         ;; Check if this is a retry attempt
         is-retry? (get-in db [:jira-templates :is-retry?])]
     (cond-> {:db (-> db
                      (assoc-in [:jira-templates->submit-template :data] updated-template)
                      (assoc-in [:jira-templates->submit-template :status]
                                (if all-requests-completed? :ready :loading)))}
       ;; If all completed and some failed, dispatch error handling
       (and all-requests-completed? any-requests-failed?)
       (assoc :fx [[:dispatch [:jira-templates->handle-cmdb-error]]])

       ;; If all completed successfully after a retry, close the loading modal and continue the flow
       (and all-requests-completed? (not any-requests-failed?) is-retry?)
       (assoc :fx [[:dispatch [:modal->close]]
                   [:dispatch [:jira-templates->continue-after-retry]]])))))

;; Add CMDB error handling events
(rf/reg-event-fx
 :jira-templates->handle-cmdb-error
 (fn [{:keys [db]} [_ context]]
   ;; Store context for retry if provided
   (let [updated-db (if context
                      (assoc-in db [:jira-templates :retry-context] context)
                      db)]
     {:db updated-db
      :dispatch [:modal->open
                 {:maxWidth "540px"
                  :content [cmdb-error/main
                            {:on-retry #(do
                                          (rf/dispatch [:modal->close])
                                          (rf/dispatch [:jira-templates->retry-cmdb-loading]))
                             :on-cancel #(rf/dispatch [:modal->close])}]}]})))

(rf/reg-event-fx
 :jira-templates->retry-cmdb-loading
 (fn [{:keys [db]} _]
   (let [template-id (get-in db [:jira-templates->submit-template :data :id])
         cmdb-items (get-in db [:jira-templates->submit-template :data :cmdb_types :items])]
     {:db (assoc-in db [:jira-templates :is-retry?] true)
      :fx [[:dispatch [:modal->open
                       {:maxWidth "540px"
                        :custom-on-click-out #(.preventDefault %)
                        :content [loading-jira-templates/main]}]]
           ;; Reset request-failed flags and retry all CMDB requests
           [:dispatch-n (for [cmdb-item cmdb-items]
                          [:jira-templates->get-cmdb-values template-id cmdb-item])]]})))

;; Add new event to continue the flow after a successful retry
(rf/reg-event-fx
 :jira-templates->continue-after-retry
 (fn [{:keys [db]} _]
   (let [template (get-in db [:jira-templates->submit-template])
         template-id (get-in template [:data :id])
         ;; Determine which flow we're in based on stored context
         context (get-in db [:jira-templates :retry-context])]
     {:db (-> db
              (assoc-in [:jira-templates :is-retry?] false)
              (update-in [:jira-templates] dissoc :retry-context))
      :fx [(if (= (:flow context) :editor)
             ;; Editor plugin flow
             [:dispatch [:editor-plugin/check-template-and-show-form
                         {:template-id template-id
                          :script (:script context)
                          :metadata (:metadata context)
                          :keep-metadata? (:keep-metadata? context)}]]
             ;; Runbooks plugin flow
             [:dispatch [:runbooks-plugin/check-template-and-show-form
                         {:template-id template-id
                          :file-name (:file-name context)
                          :params (:params context)
                          :connection-name (:connection-name context)}]])]})))

;; JIRA

(rf/reg-event-fx
 :jira-templates->get-all
 (fn [{:keys [db]} [_ _]]
   {:fx [[:dispatch
          [:fetch {:method "GET"
                   :uri "/integrations/jira/issuetemplates"
                   :on-success #(rf/dispatch [:jira-templates->set-all %])
                   :on-failure #(rf/dispatch [:jira-templates->set-all nil])}]]]
    :db (assoc db :jira-templates->list {:status :loading :data []})}))

(rf/reg-event-fx
 :jira-templates->get-by-id
 (fn [{:keys [db]} [_ id]]
   {:db (assoc db :jira-templates->active-template {:status :loading
                                                    :data {}})
    :fx [[:dispatch
          [:fetch {:method "GET"
                   :uri (str "/integrations/jira/issuetemplates/" id)
                   :on-success #(rf/dispatch [:jira-templates->set-active-template %])
                   :on-failure #(rf/dispatch [:jira-templates->set-active-template nil])}]]]}))

(rf/reg-event-fx
 :jira-templates->get-submit-template
 (fn [{:keys [db]} [_ id]]
   {:db (assoc db :jira-templates->submit-template {:status :loading :data {}})
    :fx [[:dispatch [:jira-templates->clear-submit-template]]
         [:dispatch-later
          {:ms 1000
           :dispatch [:fetch {:method "GET"
                              :uri (str "/integrations/jira/issuetemplates/" id)
                              :on-success (fn [template]
                                            (rf/dispatch [:jira-templates->set-submit-template template])
                                            (doseq [cmdb-item (get-in template [:cmdb_types :items])]
                                              (rf/dispatch [:jira-templates->get-cmdb-values id cmdb-item])))
                              :on-failure #(rf/dispatch [:jira-templates->set-submit-template nil])}]}]]}))

(rf/reg-event-fx
 :jira-templates->get-submit-template-re-run
 (fn [{:keys [db]} [_ id]]
   {:db (assoc db :jira-templates->submit-template {:status :loading :data {}})
    :fx [[:dispatch [:jira-templates->clear-submit-template]]
         [:dispatch-later
          {:ms 1000
           :dispatch [:fetch {:method "GET"
                              :uri (str "/integrations/jira/issuetemplates/" id)
                              :on-success (fn [template]
                                            (rf/dispatch [:jira-templates->set-submit-template-re-run template])
                                            (doseq [cmdb-item (get-in template [:cmdb_types :items])]
                                              (rf/dispatch [:jira-templates->get-cmdb-values id cmdb-item])))
                              :on-failure #(rf/dispatch [:jira-templates->set-submit-template-re-run nil])}]}]]}))

(rf/reg-event-db
 :jira-templates->set-all
 (fn [db [_ templates]]
   (assoc db :jira-templates->list {:status :ready :data templates})))

(rf/reg-event-db
 :jira-templates->set-active-template
 (fn [db [_ template]]
   (assoc db :jira-templates->active-template {:status :ready :data template})))


(rf/reg-event-db
 :jira-templates->set-submit-template
 (fn [db [_ template]]
   (if (empty? (get-in template [:cmdb_types :items]))
     (assoc db :jira-templates->submit-template {:status :ready :data template})
     (assoc db :jira-templates->submit-template {:status :loading :data template}))))

(rf/reg-event-fx
 :jira-templates->set-submit-template-re-run
 (fn [{:keys [db]} [_ template]]
   (let [on-template-verified (:on-template-verified db)
         has-prompts? (seq (get-in template [:prompt_types :items]))
         has-cmdb? (when-let [cmdb-items (get-in template [:cmdb_types :items])]
                     (some (fn [{:keys [value jira_values]}]
                             (when (and value jira_values)
                               (not-any? #(= value (:name %)) jira_values)))
                           cmdb-items))
         needs-form? (or has-prompts? has-cmdb?)]
     (if (empty? (get-in template [:cmdb_types :items]))
       (do
         (when on-template-verified
           (if needs-form?
             (rf/dispatch [:modal->open
                           {:content [prompt-form/main
                                      {:prompts (get-in template [:prompt_types :items])
                                       :cmdb-items (get-in template [:cmdb_types :items])
                                       :on-submit on-template-verified}]}])
             (on-template-verified nil)))
         {:db (-> db
                  (dissoc :on-template-verified)
                  (assoc :jira-templates->submit-template
                         {:status :ready :data template}))})
       {:db (assoc db :jira-templates->submit-template
                   {:status :loading :data template})}))))

(rf/reg-event-db
 :jira-templates->clear-active-template
 (fn [db _]
   (assoc db :jira-templates->active-template {:status :ready :data nil})))

(rf/reg-event-db
 :jira-templates->clear-submit-template
 (fn [db _]
   (assoc db :jira-templates->submit-template {:status :loading :data nil})))

(rf/reg-event-fx
 :jira-templates->create
 (fn [_ [_ template]]
   {:fx [[:dispatch [:jira-templates->set-submitting true]]
         [:dispatch
          [:fetch {:method "POST"
                   :uri "/integrations/jira/issuetemplates"
                   :body template
                   :on-success (fn []
                                 (rf/dispatch [:jira-templates->set-submitting false])
                                 (rf/dispatch [:jira-templates->get-all])
                                 (rf/dispatch [:navigate :jira-templates])
                                 (rf/dispatch [:jira-templates->clear-active-template]))
                   :on-failure (fn [error]
                                 (rf/dispatch [:show-snackbar {:text error :level :error}])
                                 (rf/dispatch [:jira-templates->set-submitting false]))}]]]}))

(rf/reg-event-fx
 :jira-templates->update-by-id
 (fn [_ [_ template]]
   {:fx [[:dispatch [:jira-templates->set-submitting true]]
         [:dispatch
          [:fetch {:method "PUT"
                   :uri (str "/integrations/jira/issuetemplates/" (:id template))
                   :body template
                   :on-success (fn []
                                 (rf/dispatch [:jira-templates->set-submitting false])
                                 (rf/dispatch [:jira-templates->get-all])
                                 (rf/dispatch [:navigate :jira-templates])
                                 (rf/dispatch [:jira-templates->clear-active-template]))
                   :on-failure (fn [error]
                                 (rf/dispatch [:show-snackbar {:text error :level :error}])
                                 (rf/dispatch [:jira-templates->set-submitting false]))}]]]}))

(rf/reg-event-fx
 :jira-templates->delete-by-id
 (fn [_ [_ id]]
   {:fx [[:dispatch
          [:fetch {:method "DELETE"
                   :uri (str "/integrations/jira/issuetemplates/" id)
                   :on-success (fn []
                                 (rf/dispatch [:jira-templates->get-all])
                                 (rf/dispatch [:navigate :jira-templates]))}]]]}))

(rf/reg-event-db
 :jira-templates->set-submitting
 (fn [db [_ value]]
   (assoc-in db [:jira-templates :submitting?] value)))

;; Connections
(rf/reg-event-fx
 :jira-templates->get-connections
 (fn [{:keys [db]} _]
   {:db (assoc db :jira-templates->connections-list {:status :loading :data []})
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/connections"
                             :on-success #(rf/dispatch [:jira-templates->set-connections %])
                             :on-failure #(rf/dispatch [:jira-templates->set-connections-error %])}]]]}))

(rf/reg-event-db
 :jira-templates->set-connections
 (fn [db [_ connections]]
   (assoc db :jira-templates->connections-list {:status :ready :data connections})))

(rf/reg-event-db
 :jira-templates->set-connections-error
 (fn [db [_ error]]
   (assoc db :jira-templates->connections-list {:status :error :error error :data []})))

;; Subs
(rf/reg-sub
 :jira-templates->list
 (fn [db _]
   (:jira-templates->list db)))

(rf/reg-sub
 :jira-templates->active-template
 (fn [db _]
   (:jira-templates->active-template db)))

(rf/reg-sub
 :jira-templates->submit-template
 (fn [db _]
   (:jira-templates->submit-template db)))

(rf/reg-sub
 :jira-templates->submitting?
 (fn [db]
   (get-in db [:jira-templates :submitting?])))

(rf/reg-sub
 :jira-templates->connections-list
 (fn [db _]
   (:jira-templates->connections-list db)))
