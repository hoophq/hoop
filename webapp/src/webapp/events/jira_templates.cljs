(ns webapp.events.jira-templates
  (:require
   [re-frame.core :as rf]
   [webapp.jira-templates.prompt-form :as prompt-form]
   [webapp.jira-templates.loading-jira-templates :as loading-jira-templates]
   [webapp.jira-templates.cmdb-error :as cmdb-error]
   [clojure.string :as cs]))

;; CMDB

;; Estado adicional para paginação e busca de CMDB
(rf/reg-event-db
 :jira-templates->set-cmdb-pagination
 (fn [db [_ cmdb-item pagination]]
   (assoc-in db [:jira-templates :cmdb-pagination (:jira_object_type cmdb-item)] pagination)))

(rf/reg-event-db
 :jira-templates->set-cmdb-search
 (fn [db [_ cmdb-item search-term]]
   (assoc-in db [:jira-templates :cmdb-search (:jira_object_type cmdb-item)] search-term)))

(rf/reg-sub
 :jira-templates->cmdb-pagination
 (fn [db [_ object-type]]
   (get-in db [:jira-templates :cmdb-pagination object-type]
           {:page 1 :per-page 50 :total-items 0})))

(rf/reg-sub
 :jira-templates->cmdb-search
 (fn [db [_ object-type]]
   (get-in db [:jira-templates :cmdb-search object-type] "")))

(rf/reg-sub
 :jira-templates->cmdb-loading?
 (fn [db [_ object-type]]
   (get-in db [:jira-templates :cmdb-loading object-type] false)))

(rf/reg-event-db
 :jira-templates->set-cmdb-loading
 (fn [db [_ object-type loading?]]
   (assoc-in db [:jira-templates :cmdb-loading object-type] loading?)))

(rf/reg-event-db
 :jira-templates->update-cmdb-value
 (fn [db [_ cmdb-item value]]
   (let [current-template (get-in db [:jira-templates->submit-template :data])
         cmdb-items (get-in current-template [:cmdb_types :items])
         updated-cmdb-items (map (fn [item]
                                   (if (= (:jira_field item) (:jira_field cmdb-item))
                                     (assoc item :value value)
                                     item))
                                 cmdb-items)
         updated-template (assoc-in current-template [:cmdb_types :items] updated-cmdb-items)]
     (assoc-in db [:jira-templates->submit-template :data] updated-template))))

(rf/reg-event-fx
 :jira-templates->get-cmdb-values
 (fn [{:keys [db]} [_ template-id cmdb-item & [page search-term]]]
   (let [page (or page 1)
         search-term (or search-term "")
         pagination (get-in db [:jira-templates :cmdb-pagination (:jira_object_type cmdb-item)]
                            {:page page :per-page 50})]
     {:fx [[:dispatch [:jira-templates->set-cmdb-loading (:jira_object_type cmdb-item) true]]
           [:dispatch
            [:fetch {:method "GET"
                     :uri (str "/integrations/jira/assets/objects?"
                               "object_type_id=" (:jira_object_type cmdb-item)
                               "&object_schema_id=" (:jira_object_schema_id cmdb-item)
                               "&offset=" (* (- page 1) (:per-page pagination))
                               "&limit=" (:per-page pagination)
                               (when-not (empty? search-term)
                                 (str "&name=" (js/encodeURIComponent search-term))))
                     :on-success (fn [response]
                                   (rf/dispatch [:jira-templates->set-cmdb-loading (:jira_object_type cmdb-item) false])
                                   (rf/dispatch [:jira-templates->set-cmdb-pagination
                                                 cmdb-item
                                                 {:page page
                                                  :per-page (:per-page pagination)
                                                  :total-items (:total response)}])
                                   (rf/dispatch [:jira-templates->merge-cmdb-values cmdb-item (:values response)]))
                     :on-failure (fn [error]
                                   (rf/dispatch [:jira-templates->set-cmdb-loading (:jira_object_type cmdb-item) false])
                                   (rf/dispatch [:jira-templates->merge-cmdb-values cmdb-item nil]))}]]]})))

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
                                         (assoc :request-failed failed-request?)
                                         (assoc :jira_values (merge value)))
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
                                              (rf/dispatch [:jira-templates->set-cmdb-loading (:jira_object_type cmdb-item) true])
                                              (rf/dispatch [:jira-templates->get-cmdb-values id cmdb-item 1 ""])))
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
                                              (rf/dispatch [:jira-templates->set-cmdb-loading (:jira_object_type cmdb-item) true])
                                              (rf/dispatch [:jira-templates->get-cmdb-values id cmdb-item 1 ""])))
                              :on-failure #(rf/dispatch [:jira-templates->set-submit-template-re-run nil])}]}]]}))

(rf/reg-event-db
 :jira-templates->set-all
 (fn [db [_ templates]]
   (assoc db :jira-templates->list {:status :ready :data templates})))

(rf/reg-event-db
 :jira-templates->set-active-template
 (fn [db [_ template]]
   (assoc db :jira-templates->active-template
          {:status :ready
           :data (merge {:connections nil :connections-loading false :connections-error nil} template)})))


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
                                 (rf/dispatch [:show-snackbar {:text "Failed to create JIRA template" :level :error :details error}])
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
                                 (rf/dispatch [:show-snackbar {:text "Failed to update JIRA template" :level :error :details error}])
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
 :jira-templates->active-template-id
 (fn [db _]
   (get-in db [:jira-templates->active-template :data :id])))

(rf/reg-sub
 :jira-templates->submit-template
 (fn [db _]
   (:jira-templates->submit-template db)))

(rf/reg-sub
 :jira-templates->submit-template-cmdb-items
 (fn [db _]
   (get-in db [:jira-templates->submit-template :data :cmdb_types :items])))

(rf/reg-sub
 :jira-templates->submit-template-id
 (fn [db _]
   (get-in db [:jira-templates->submit-template :data :id])))

(rf/reg-sub
 :jira-templates->submitting?
 (fn [db]
   (get-in db [:jira-templates :submitting?])))


(rf/reg-event-fx
 :jira-templates/get-selected-connections
 (fn [{:keys [db]} [_ connection-ids]]
   (if (seq connection-ids)
     (let [page-size 30
           base-uri "/connections"
           chunks (partition-all page-size connection-ids)
           num-batches (count chunks)
           mk-uri (fn [ids]
                    (let [query-params [(str "connection_ids=" (cs/join "," ids))
                                        "page=1"
                                        (str "page_size=" page-size)]]
                      (str base-uri "?" (cs/join "&" query-params))))
           fx-requests (mapv (fn [ids]
                               [:dispatch
                                [:fetch {:method "GET"
                                         :uri (mk-uri ids)
                                         :on-success (fn [response]
                                                       (rf/dispatch [:jira-templates/accumulate-selected-connections (:data response)]))
                                         :on-failure (fn [error]
                                                       (rf/dispatch [:jira-templates/accumulate-selected-connections-error error]))}]])
                             chunks)]
       {:db (update-in db [:jira-templates->active-template :data] merge
                       {:connections-loading {:remaining num-batches :acc [] :errors []}})
        :fx fx-requests})
     {:db (update-in db [:jira-templates->active-template :data] merge
                     {:connections [] :connections-loading {:remaining 0 :acc [] :errors []}})})))

(rf/reg-event-fx
 :jira-templates/accumulate-selected-connections
 (fn [{:keys [db]} [_ connections]]
   (let [{:keys [remaining acc]} (get-in db [:jira-templates->active-template :data :connections-loading] {:remaining 0 :acc []})
         new-remaining (dec remaining)
         new-acc (into acc connections)]
     (if (pos? new-remaining)
       {:db (update-in db [:jira-templates->active-template :data] merge
                       {:connections-loading {:remaining new-remaining
                                              :acc new-acc
                                              :errors (:errors (get-in db [:jira-templates->active-template :data :connections-loading]))}})}
       {:db (update-in db [:jira-templates->active-template :data] merge
                       {:connections-loading false})
        :fx [[:dispatch [:jira-templates/set-selected-connections new-acc]]]}))))

(rf/reg-event-fx
 :jira-templates/accumulate-selected-connections-error
 (fn [{:keys [db]} [_ error]]
   (let [{:keys [remaining acc errors]} (get-in db [:jira-templates->active-template :data :connections-loading] {:remaining 0 :acc [] :errors []})
         new-remaining (dec remaining)
         new-errors (conj (vec errors) error)]
     (if (pos? new-remaining)
       {:db (update-in db [:jira-templates->active-template :data] merge
                       {:connections-loading {:remaining new-remaining
                                             :acc acc
                                             :errors new-errors}})}
       {:db (update-in db [:jira-templates->active-template :data] merge
                       {:connections-loading false})
        :fx [[:dispatch [:jira-templates/set-selected-connections-error new-errors]]]}))))

(rf/reg-event-db
 :jira-templates/set-selected-connections
 (fn [db [_ connections]]
   (let [filtered-connections (mapv #(select-keys % [:id :name]) connections)]
     (assoc-in db [:jira-templates->active-template :data :connections] filtered-connections))))

(rf/reg-event-db
 :jira-templates/set-selected-connections-error
 (fn [db [_ error]]
   (update-in db [:jira-templates->active-template :data] merge
              {:connections [] :connections-error error})))
