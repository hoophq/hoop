(ns webapp.events.jira-templates
  (:require
   [re-frame.core :as rf]))

(def mock-templates
  [{:id "template_1"
    :name "template_1"
    :description "Description with dummy data as reference."
    :project_key "HT"
    :issue_type_name "Hoop Task"
    :mapping_types {:items [{:description "Hoop Connection Name"
                             :type "preset"
                             :value "session.connection"
                             :jira_field "customfield_10050"}
                            {:description "Hoop Session ID"
                             :type "preset"
                             :value "session.id"
                             :jira_field "customfield_10051"}]}
    :prompt_types {:items [{:description "Squad"
                            :required true
                            :label "Squad Name"
                            :jira_field "customfield_10059"}]}}
   {:id "banking+prod_mongodb"
    :name "banking+prod_mongodb"
    :description "Default rules for Banking squad's production mongodb databases."
    :project_key "HT"
    :issue_type_name "Hoop Task"
    :mapping_types {:items [{:description "Hoop Review ID"
                             :type "custom"
                             :value "review-id-test-it hey ho"
                             :jira_field "customfield_10052"}]}
    :prompt_types {:items []}}])

(rf/reg-event-fx
 :jira-templates->get-all
 (fn [{:keys [db]} [_ _]]
   ;; TODO: Uncomment when API is ready
   #_{:fx [[:dispatch
            [:fetch {:method "GET"
                     :uri "/jira-templates"
                     :on-success #(rf/dispatch [:jira-templates->set-all %])
                     :on-failure #(rf/dispatch [:jira-templates->set-all nil])}]]]
      :db (assoc db :jira-templates->list {:status :loading :data []})}

   ;; Mock response
   {:db (assoc db :jira-templates->list
               {:status :ready
                :data mock-templates})}))

(rf/reg-event-fx
 :jira-templates->get-by-id
 (fn [{:keys [db]} [_ id]]
   ;; TODO: Uncomment quando API estiver pronta
   #_{:db (assoc db :jira-templates->active-template {:status :loading
                                                      :data {}})
      :fx [[:dispatch
            [:fetch {:method "GET"
                     :uri (str "/jira-templates/" id)
                     :on-success #(rf/dispatch [:jira-templates->set-active-template %])
                     :on-failure #(rf/dispatch [:jira-templates->set-active-template nil])}]]]}

   ;; Mock: procura o template nos dados mock
   (let [template (first (filter #(= (:id %) id) mock-templates))]
     {:db (assoc db :jira-templates->active-template
                 {:status :ready
                  :data template})})))

(rf/reg-event-db
 :jira-templates->set-all
 (fn [db [_ templates]]
   (assoc db :jira-templates->list {:status :ready :data templates})))

(rf/reg-event-db
 :jira-templates->set-active-template
 (fn [db [_ template]]
   (assoc db :jira-templates->active-template {:status :ready :data template})))

(rf/reg-event-fx
 :jira-templates->create
 (fn [_ [_ template]]
   (js/console.log "Create Template Payload:" (clj->js template))  ;; Log do payload
   {:fx [[:dispatch
          [:fetch {:method "POST"
                   :uri "/jira-templates"
                   :body template
                   :on-success (fn []
                                 (rf/dispatch [:jira-templates->get-all])
                                 (rf/dispatch [:navigate :jira-templates]))
                   :on-failure #(println :jira-templates->create template %)}]]]}))

(rf/reg-event-fx
 :jira-templates->update-by-id
 (fn [_ [_ template]]
   (js/console.log "Update Template Payload:" (clj->js template))  ;; Log do payload
   {:fx [[:dispatch
          [:fetch {:method "PUT"
                   :uri (str "/jira-templates/" (:id template))
                   :body template
                   :on-success (fn []
                                 (rf/dispatch [:jira-templates->get-all])
                                 (rf/dispatch [:navigate :jira-templates]))
                   :on-failure #(println :jira-templates->update-by-id template %)}]]]}))

(rf/reg-event-fx
 :jira-templates->delete-by-id
 (fn [_ [_ id]]
   {:fx [[:dispatch
          [:fetch {:method "DELETE"
                   :uri (str "/jira-templates/" id)
                   :on-success (fn []
                                 (rf/dispatch [:jira-templates->get-all])
                                 (rf/dispatch [:navigate :jira-templates]))
                   :on-failure #(println :jira-templates->delete-by-id id %)}]]]}))

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
 :jira-templates->template-by-id
 :<- [:jira-templates->list]
 (fn [templates [_ id]]
   (when-let [template-list (:data templates)]
     (first (filter #(= (:id %) id) template-list)))))
