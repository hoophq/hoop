(ns webapp.events.jira-templates
  (:require
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.jira-templates.cascade :as cascade]
   [webapp.jira-templates.prompt-form :as prompt-form]
   [webapp.jira-templates.loading-jira-templates :as loading-jira-templates]
   [webapp.jira-templates.cmdb-error :as cmdb-error]))

;; CMDB

;; Estado adicional para paginação e busca de CMDB
;; All per-row CMDB state (pagination, search, loading, request ids) is keyed
;; by the row's jira_field: object types are not unique across rows, and two
;; rows sharing a type may carry different field-config AQL filters.
(rf/reg-event-db
 :jira-templates->set-cmdb-pagination
 (fn [db [_ cmdb-item pagination]]
   (assoc-in db [:jira-templates :cmdb-pagination (:jira_field cmdb-item)] pagination)))

(rf/reg-event-db
 :jira-templates->set-cmdb-search
 (fn [db [_ cmdb-item search-term]]
   (assoc-in db [:jira-templates :cmdb-search (:jira_field cmdb-item)] search-term)))

(rf/reg-sub
 :jira-templates->cmdb-pagination
 (fn [db [_ jira-field]]
   (get-in db [:jira-templates :cmdb-pagination jira-field]
           {:page 1 :per-page 50 :total-items 0})))

(rf/reg-sub
 :jira-templates->cmdb-search
 (fn [db [_ jira-field]]
   (get-in db [:jira-templates :cmdb-search jira-field] "")))

(rf/reg-sub
 :jira-templates->cmdb-loading?
 (fn [db [_ jira-field]]
   (get-in db [:jira-templates :cmdb-loading jira-field] false)))

(rf/reg-event-db
 :jira-templates->set-cmdb-loading
 (fn [db [_ jira-field loading?]]
   (assoc-in db [:jira-templates :cmdb-loading jira-field] loading?)))

;; The AQL configuration of each row's Assets custom field (the same filters
;; Jira's portal applies) drives the dropdown cascade. Fetched once per prompt.
(rf/reg-event-fx
 :jira-templates->get-cmdb-fieldconfigs
 (fn [{:keys [db]} [_ cmdb-items]]
   (let [fields (distinct (keep :jira_field cmdb-items))]
     (if (empty? fields)
       {:db (assoc-in db [:jira-templates :cmdb-fieldconfigs] {})}
       {:db (assoc-in db [:jira-templates :cmdb-fieldconfigs] {})
        :fx [[:dispatch
              [:fetch {:method "GET"
                       :uri (str "/integrations/jira/assets/fieldconfigs?jira_fields="
                                 (js/encodeURIComponent (cs/join "," fields)))
                       :on-success #(rf/dispatch [:jira-templates->set-cmdb-fieldconfigs (:items %)])
                       ;; No cascade is better than a broken prompt: fall back
                       ;; to unfiltered dropdowns when the config lookup fails.
                       :on-failure #(rf/dispatch [:jira-templates->set-cmdb-fieldconfigs []])}]]]}))))

(rf/reg-event-fx
 :jira-templates->set-cmdb-fieldconfigs
 (fn [{:keys [db]} [_ config-items]]
   (let [fieldconfigs (cascade/index-configs config-items)
         items (get-in db [:jira-templates->submit-template :data :cmdb_types :items])
         template-id (get-in db [:jira-templates->submit-template :data :id])
         ;; Every configured row is refreshed: the field configuration
         ;; replaces the template's object type, so the fetch that already
         ;; went out with the legacy scope must be redone (or emptied, when
         ;; the row waits on an upstream selection). Unconfigured fields keep
         ;; their in-flight fetch.
         configured (filter #(some? (cascade/filter-for % items fieldconfigs)) items)]
     {:db (assoc-in db [:jira-templates :cmdb-fieldconfigs] fieldconfigs)
      :fx (vec (for [item configured]
                 [:dispatch [:jira-templates->get-cmdb-values template-id item 1 "" true]]))})))

(rf/reg-sub
 :jira-templates->cmdb-fieldconfigs
 (fn [db _]
   (get-in db [:jira-templates :cmdb-fieldconfigs] {})))

(rf/reg-event-fx
 :jira-templates->update-cmdb-value
 (fn [{:keys [db]} [_ cmdb-item value]]
   (let [current-template (get-in db [:jira-templates->submit-template :data])
         cmdb-items (get-in current-template [:cmdb_types :items])
         ;; The searchbox hands us the selected object id; keep the matching
         ;; name so sibling rows can substitute it into their AQL filters.
         selected-name (:name (first (filter #(= (:id %) value)
                                             (:jira_values cmdb-item))))
         fieldconfigs (get-in db [:jira-templates :cmdb-fieldconfigs] {})
         dependents (cascade/all-dependents cmdb-item cmdb-items fieldconfigs)
         dependent-fields (set (map :jira_field dependents))
         updated-cmdb-items (map (fn [item]
                                   (cond
                                     (= (:jira_field item) (:jira_field cmdb-item))
                                     (assoc item :value value :selected-name selected-name)

                                     ;; A row filtered by this selection loses
                                     ;; its current pick AND its loaded options:
                                     ;; they belong to the previous upstream
                                     ;; selection, and must not stay selectable
                                     ;; if the refetch below fails.
                                     (contains? dependent-fields (:jira_field item))
                                     (assoc item :value nil :selected-name nil :jira_values [])

                                     :else item))
                                 cmdb-items)
         updated-template (assoc-in current-template [:cmdb_types :items] updated-cmdb-items)
         template-id (:id current-template)]
     {:db (assoc-in db [:jira-templates->submit-template :data] updated-template)
      ;; Dependents refetch as cascade fetches (last arg): a failure keeps
      ;; the options already loaded instead of opening the retry modal, and
      ;; any stale search term is dropped alongside the stale pick.
      :fx (vec (mapcat (fn [dep]
                         [[:dispatch [:jira-templates->set-cmdb-search dep ""]]
                          [:dispatch [:jira-templates->get-cmdb-values template-id dep 1 "" true]]])
                       dependents))})))

(rf/reg-event-fx
 :jira-templates->get-cmdb-values
 (fn [{:keys [db]} [_ template-id cmdb-item & [page search-term cascade?]]]
   (let [page (or page 1)
         search-term (or search-term "")
         object-type (:jira_object_type cmdb-item)
         field (:jira_field cmdb-item)
         pagination (get-in db [:jira-templates :cmdb-pagination field]
                            {:page page :per-page 50})
         ;; Resolve against the freshest row state: the component may hand us
         ;; an item captured before the latest sibling selection.
         cmdb-items (get-in db [:jira-templates->submit-template :data :cmdb_types :items])
         current-item (or (first (filter #(= (:jira_field %) (:jira_field cmdb-item)) cmdb-items))
                          cmdb-item)
         fieldconfigs (get-in db [:jira-templates :cmdb-fieldconfigs] {})
         field-filter (cascade/filter-for current-item cmdb-items fieldconfigs)
         ;; The field configuration owns the scope when present: its AQL
         ;; replaces the template's object type and its schema wins too.
         schema-id (or (:object-schema-id field-filter) (:jira_object_schema_id cmdb-item))
         ;; Monotonic id per row (jira_field): only the latest in-flight fetch
         ;; may update the dropdown, so a slower older response cannot
         ;; overwrite AQL-filtered options with unfiltered ones. Bumped for
         ;; the pending case too, to invalidate anything already in flight.
         request-id (inc (get-in db [:jira-templates :cmdb-request-id field] 0))
         db (assoc-in db [:jira-templates :cmdb-request-id field] request-id)]
     (if (= :pending field-filter)
       ;; Configured, but the dependent-field filter has no upstream
       ;; selection yet: the field accepts nothing, so offer nothing.
       {:db db
        :fx [[:dispatch [:jira-templates->set-cmdb-loading field false]]
             [:dispatch [:jira-templates->set-cmdb-pagination
                         cmdb-item
                         {:page 1 :per-page (:per-page pagination) :total-items 0}]]
             [:dispatch [:jira-templates->merge-cmdb-values cmdb-item []]]]}
       {:db db
        :fx [[:dispatch [:jira-templates->set-cmdb-loading field true]]
             [:dispatch
              [:fetch {:method "GET"
                       :uri (str "/integrations/jira/assets/objects?"
                                 "object_type_id=" object-type
                                 "&object_schema_id=" schema-id
                                 "&offset=" (* (- page 1) (:per-page pagination))
                                 "&limit=" (:per-page pagination)
                                 (when-not (empty? search-term)
                                   (str "&name=" (js/encodeURIComponent search-term)))
                                 (when-not (empty? (:aql field-filter))
                                   (str "&aql=" (js/encodeURIComponent (:aql field-filter)))))
                       :on-success (fn [response]
                                     (rf/dispatch [:jira-templates->cmdb-values-success cmdb-item request-id
                                                   {:page page
                                                    :per-page (:per-page pagination)
                                                    :response response}]))
                       :on-failure (fn [_error]
                                     (rf/dispatch [:jira-templates->cmdb-values-failure cmdb-item request-id cascade?]))}]]]}))))

(rf/reg-event-fx
 :jira-templates->cmdb-values-success
 (fn [{:keys [db]} [_ cmdb-item request-id {:keys [page per-page response]}]]
   (let [field (:jira_field cmdb-item)]
     (if (not= request-id (get-in db [:jira-templates :cmdb-request-id field]))
       {} ;; stale: a newer fetch for this row is in flight
       {:fx [[:dispatch [:jira-templates->set-cmdb-loading field false]]
             [:dispatch [:jira-templates->set-cmdb-pagination
                         cmdb-item
                         {:page page
                          :per-page per-page
                          :total-items (:total response)}]]
             [:dispatch [:jira-templates->merge-cmdb-values cmdb-item (:values response)]]]}))))

(rf/reg-event-fx
 :jira-templates->cmdb-values-failure
 (fn [{:keys [db]} [_ cmdb-item request-id cascade?]]
   (let [field (:jira_field cmdb-item)]
     (cond
       (not= request-id (get-in db [:jira-templates :cmdb-request-id field]))
       {} ;; stale: a newer fetch for this row is in flight

       ;; A failed cascade refetch degrades to whatever the row currently
       ;; offers (dependents were already emptied at invalidation) instead of
       ;; escalating to the retry modal, which would remount the form and
       ;; discard everything the user already typed.
       cascade?
       {:fx [[:dispatch [:jira-templates->set-cmdb-loading field false]]]}

       :else
       {:fx [[:dispatch [:jira-templates->set-cmdb-loading field false]]
             [:dispatch [:jira-templates->merge-cmdb-values cmdb-item nil]]]}))))

(rf/reg-event-fx
 :jira-templates->merge-cmdb-values
 (fn [{:keys [db]} [_ cmdb-item value]]
   (let [current-template (get-in db [:jira-templates->submit-template :data])
         cmdb-items (get-in current-template [:cmdb_types :items])
         ;; Track if this was a failed request
         failed-request? (nil? value)
         ;; Update items
         updated-cmdb-items (map (fn [item]
                                   (if (= (:jira_field item) (:jira_field cmdb-item))
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
             ;; Runbooks flow
             [:dispatch [:runbooks/check-jira-template-and-show-form
                         {:template-id template-id
                          :file-name (:file-name context)
                          :params (:params context)
                          :connection-name (:connection-name context)
                          :repository (:repository context)
                          :ref-hash (:ref-hash context)}]])]})))

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
                                            (rf/dispatch [:jira-templates->get-cmdb-fieldconfigs (get-in template [:cmdb_types :items])])
                                            (doseq [cmdb-item (get-in template [:cmdb_types :items])]
                                              (rf/dispatch [:jira-templates->set-cmdb-loading (:jira_field cmdb-item) true])
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
                                            (rf/dispatch [:jira-templates->get-cmdb-fieldconfigs (get-in template [:cmdb_types :items])])
                                            (doseq [cmdb-item (get-in template [:cmdb_types :items])]
                                              (rf/dispatch [:jira-templates->set-cmdb-loading (:jira_field cmdb-item) true])
                                              (rf/dispatch [:jira-templates->get-cmdb-values id cmdb-item 1 ""])))
                              :on-failure #(rf/dispatch [:jira-templates->set-submit-template-re-run nil])}]}]]}))

(rf/reg-event-db
 :jira-templates->set-all
 (fn [db [_ templates]]
   (assoc db :jira-templates->list {:status :ready :data templates})))

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
 :jira-templates->clear-submit-template
 (fn [db _]
   (assoc db :jira-templates->submit-template {:status :loading :data nil})))

;; Subs
(rf/reg-sub
 :jira-templates->list
 (fn [db _]
   (:jira-templates->list db)))

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

