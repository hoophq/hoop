(ns webapp.events.jira-integration
  (:require [re-frame.core :as rf]))

(rf/reg-event-fx
 :jira-integration->get
 (fn
   [{:keys [db]} [_]]
   {:db (assoc db :jira-integration->details {:loading true :data {}})
    :fx [[:dispatch
          [:fetch {:method "GET"
                   :uri (str "/integrations/jira")
                   :on-success (fn [jira-details]
                                 (rf/dispatch [:jira-integration->set-jira-details jira-details]))}]]]}))

(rf/reg-event-fx
 :jira-integration->set-jira-details
 (fn
   [{:keys [db]} [_ jira-details]]
   {:db (assoc db :jira-integration->details {:loading false :data jira-details})}))

(rf/reg-event-fx
 :jira-integration->create
 (fn
   [{:keys [db]} [_ jira-config]]
   {:fx [[:dispatch [:fetch
                     {:method "POST"
                      :uri "/integrations/jira"
                      :body jira-config
                      :on-success (fn [_]
                                    (rf/dispatch [:jira-integration->get])
                                    (rf/dispatch [:show-snackbar {:level :success
                                                                  :text "Jira integration created!"}]))}]]]}))


(rf/reg-event-fx
 :jira-integration->update
 (fn
   [{:keys [db]} [_ jira-config]]
   {:fx [[:dispatch [:fetch
                     {:method "PUT"
                      :uri (str "/integrations/jira")
                      :body jira-config
                      :on-success (fn []
                                    (rf/dispatch [:show-snackbar
                                                  {:level :success
                                                   :text "Jira integration updated!"}])
                                    (rf/dispatch [:jira-integration->get]))}]]]}))

(rf/reg-sub
 :jira-integration->integration-enabled?
 :<- [:jira-integration->details]
 (fn [integration [_]]
   (= (-> integration :data :status) "enabled")))
