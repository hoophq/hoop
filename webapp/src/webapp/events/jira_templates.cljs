(ns webapp.events.jira-templates
  (:require
   [re-frame.core :as rf]))

(def mock-template-response
  {:id "123"
   :name "Template de Teste"
   :description "Template para testes"
   :prompt_types {:items [{:label "Descrição"
                           :jira_field "customfield_10052"
                           :required true
                           :description "Descrição do ticket"}]}
   :cmdb_types {:items [{:description "Product Field"
                         :jira_field "customfield_10109"
                         :jira_object_type "Product"
                         :required true
                         :value "pix"
                         :jira_values [{:id "c1ee84ab-76c8-40d9-a956-13a705d4e605:5"
                                        :name "banking"}
                                       {:id "c1ee84ab-76c8-40d9-a956-13a705d4e605:4"
                                        :name "pix"
                                        :label "products"}]}
                        {:description "Another Field"
                         :jira_field "customfield_10110"
                         :jira_object_type "Service"
                         :required true
                         :value "invalid-value" ; valor que não existe nos jira_values
                         :jira_values [{:id "crwrwrwerw-40d9-a956-13a705d4e605:5"
                                        :name "service-1"}
                                       {:id "service-id-2"
                                        :name "service-2"}]}]}})

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
                   :uri (str "/integrations/jira/issuetemplates/" id "?expand=cmdbtype-values")
                   :on-success #(rf/dispatch [:jira-templates->set-active-template %])
                   :on-failure #(rf/dispatch [:jira-templates->set-active-template nil])}]]]}))

(rf/reg-event-fx
 :jira-templates->get-submit-template
 (fn [{:keys [db]} [_ id]]

   {:db (assoc db :jira-templates->submit-template {:status :loading
                                                    :data {}})
       ;; Simula uma chamada assíncrona com timeout
    :fx [[:dispatch-later
          {:ms 500
           :dispatch [:jira-templates->set-submit-template mock-template-response]}]]}

   #_{:db (assoc db :jira-templates->submit-template {:status :loading
                                                      :data {}})
      :fx [[:dispatch
            [:fetch {:method "GET"
                     :uri (str "/integrations/jira/issuetemplates/" id "?expand=cmdbtype-values")
                     :on-success #(rf/dispatch [:jira-templates->set-submit-template %])
                     :on-failure #(rf/dispatch [:jira-templates->set-submit-template nil])}]]]}))

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
   (assoc db :jira-templates->submit-template {:status :ready :data template})))

(rf/reg-event-db
 :jira-templates->clear-submit-template
 (fn [db _]
   (assoc db :jira-templates->submit-template {:status :ready :data nil})))

(rf/reg-event-fx
 :jira-templates->create
 (fn [_ [_ template]]
   {:fx [[:dispatch
          [:fetch {:method "POST"
                   :uri "/integrations/jira/issuetemplates"
                   :body template
                   :on-success (fn []
                                 (rf/dispatch [:jira-templates->get-all])
                                 (rf/dispatch [:navigate :jira-templates]))
                   :on-failure #(println :jira-templates->create template %)}]]]}))

(rf/reg-event-fx
 :jira-templates->update-by-id
 (fn [_ [_ template]]
   {:fx [[:dispatch
          [:fetch {:method "PUT"
                   :uri (str "/integrations/jira/issuetemplates/" (:id template))
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
                   :uri (str "/integrations/jira/issuetemplates/" id)
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
 :jira-templates->submit-template
 (fn [db _]
   (:jira-templates->submit-template db)))
