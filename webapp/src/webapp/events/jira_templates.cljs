(ns webapp.events.jira-templates
  (:require
   [re-frame.core :as rf]))

;; Mock data for testing
(def mock-templates
  [{:id "1"
    :name "banking+prod_mongodb"
    :description "Default rules for Banking squad's production mongodb databases."
    :jira_template [{:type "hoop.dev"
                     :value "session_id"
                     :jira_field "ID da Sessão"
                     :description "Relaciona Session ID do hoop.dev no campo ID da Sessão no Jira"}
                    {:type "custom"
                     :value "Banking"
                     :jira_field "custom_0072"
                     :description "Adiciona o valor 'Banking' no campo Squad nos cards do Jira"}]}
   {:id "2"
    :name "payments+postgres"
    :description "Template for payments team postgres access"
    :jira_template [{:type "hoop.dev"
                     :value "database_name"
                     :jira_field "Database"
                     :description "Maps database name to Jira field"}]}])

(rf/reg-event-fx
 :jira-templates->get-all
 (fn [{:keys [db]} [_ _]]
   ;; TODO: Uncomment when API is ready
   #_{:fx [[:dispatch
            [:fetch {:method "GET"
                     :uri "/jira-templates"
                     :on-success (fn [templates]
                                   (rf/dispatch [:jira-templates->set-all templates]))
                     :on-failure (fn [error]
                                   (rf/dispatch [:jira-templates->set-all nil error]))}]]]
      :db (assoc db :jira-templates->list {:status :loading
                                           :data []})}

   ;; Using mock data
   {:db (assoc db :jira-templates->list {:status :ready
                                         :data mock-templates})}))

(rf/reg-event-fx
 :jira-templates->get-by-id
 (fn [{:keys [db]} [_ id]]
   ;; TODO: Uncomment when API is ready
   #_{:fx [[:dispatch
            [:fetch {:method "GET"
                     :uri (str "/jira-templates/" id)
                     :on-success (fn [template]
                                   (rf/dispatch [:jira-templates->set-active-template template]))
                     :on-failure (fn [error]
                                   (rf/dispatch [:jira-templates->set-all nil error]))}]]]
      :db (assoc db :jira-templates->active-template {:status :loading
                                                      :data {}})}

   ;; Using mock data
   {:db (assoc db :jira-templates->active-template
               {:status :ready
                :data (first (filter #(= (:id %) id) mock-templates))})}))

(rf/reg-event-db
 :jira-templates->set-all
 (fn [db [_ templates]]
   (assoc db :jira-templates->list {:status :ready :data templates})))

(defn remove-empty-rules [rules]
  (remove (fn [rule]
            (or (empty? (:type rule))
                (empty? (:value rule))
                (empty? (:jira_field rule))))
          rules))

(defn sanitize-template [template]
  (update template :jira_template remove-empty-rules))

(rf/reg-event-fx
 :jira-templates->create
 (fn [_ [_ template]]
   ;; TODO: Uncomment when API is ready
   #_{:fx [[:dispatch
            [:fetch {:method "POST"
                     :uri "/jira-templates"
                     :body (sanitize-template template)
                     :on-success (fn []
                                   (rf/dispatch [:jira-templates->get-all])
                                   (rf/dispatch [:navigate :jira-templates]))
                     :on-failure (fn [error]
                                   (println :jira-templates->create template error))}]]]}

   ;; Mock success response
   {:fx [[:dispatch [:jira-templates->get-all]]
         [:dispatch [:navigate :jira-templates]]]}))

(rf/reg-event-fx
 :jira-templates->update-by-id
 (fn [_ [_ template]]
   ;; TODO: Uncomment when API is ready
   #_{:fx [[:dispatch
            [:fetch {:method "PUT"
                     :uri (str "/jira-templates/" (:id template))
                     :body (sanitize-template template)
                     :on-success (fn []
                                   (rf/dispatch [:jira-templates->get-all])
                                   (rf/dispatch [:navigate :jira-templates]))
                     :on-failure (fn [error]
                                   (println :jira-templates->update-by-id template error))}]]]}

   ;; Mock success response
   {:fx [[:dispatch [:jira-templates->get-all]]
         [:dispatch [:navigate :jira-templates]]]}))

(rf/reg-event-fx
 :jira-templates->delete-by-id
 (fn [_ [_ id]]
   ;; TODO: Uncomment when API is ready
   #_{:fx [[:dispatch
            [:fetch {:method "DELETE"
                     :uri (str "/jira-templates/" id)
                     :on-success (fn []
                                   (rf/dispatch [:jira-templates->get-all])
                                   (rf/dispatch [:navigate :jira-templates]))
                     :on-failure (fn [error]
                                   (println :jira-templates->delete-by-id id error))}]]]}

   ;; Mock success response
   {:fx [[:dispatch [:jira-templates->get-all]]
         [:dispatch [:navigate :jira-templates]]]}))

;; Rest of the events remain the same as they handle local state
(rf/reg-event-fx
 :jira-templates->set-active-template
 (fn [{:keys [db]} [_ template]]
   (let [{:keys [id name description jira_template]} template
         template-schema {:id (or id "")
                          :name (or name "")
                          :description (or description "")
                          :jira_template (if (seq jira_template)
                                           (mapv #(assoc % :selected false) jira_template)
                                           [{:type "" :value "" :jira_field "" :description "" :selected false}])}]
     {:db (assoc db :jira-templates->active-template {:status :ready
                                                      :data template-schema})})))

(rf/reg-event-fx
 :jira-templates->clear-active-template
 (fn [{:keys [db]} [_]]
   {:db (assoc db :jira-templates->active-template {:status :loading
                                                    :data {}})
    :fx [[:dispatch
          [:jira-templates->set-active-template {:id ""
                                                 :name ""
                                                 :description ""
                                                 :jira_template [{:type ""
                                                                  :value ""
                                                                  :jira_field ""
                                                                  :description ""
                                                                  :selected false}]}]]]}))

;; Subscriptions
(rf/reg-sub
 :jira-templates->list
 (fn [db _]
   (get-in db [:jira-templates->list])))

(rf/reg-sub
 :jira-templates->active-template
 (fn [db _]
   (get-in db [:jira-templates->active-template])))
