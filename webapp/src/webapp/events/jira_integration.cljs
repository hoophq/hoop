(ns webapp.events.jira-integration
  (:require [re-frame.core :as rf]))

(rf/reg-event-fx
 :jira-integration->get
 (fn
   [{:keys [db]} [_]]
   {:db (assoc db :jira-integration->details {:loading true :data {}})
    :fx [[:dispatch
          [:fetch {:method "GET"
                   :uri "/integrations/jira"
                   :on-success (fn [jira-details]
                                 (rf/dispatch [:jira-integration->set-jira-details jira-details]))}]]]}))

(rf/reg-event-fx
 :jira-integration->set-jira-details
 (fn
   [{:keys [db]} [_ jira-details]]
   {:db (assoc db :jira-integration->details {:loading false :data jira-details})}))

(rf/reg-sub
 :jira-integration->integration-enabled?
 :<- [:jira-integration->details]
 (fn [integration [_]]
   (= (-> integration :data :status) "enabled")))
