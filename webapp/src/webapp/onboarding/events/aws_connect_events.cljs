(ns webapp.onboarding.events.aws-connect-events
  (:require [re-frame.core :as rf]
            [webapp.onboarding.mock-data :as mock]))

;; Initialize AWS Connect state
(rf/reg-event-db
 :aws-connect/initialize-state
 (fn [db _]
   (assoc db :aws-connect {:current-step :credentials
                           :status nil
                           :error nil
                           :credentials mock/mock-aws-credentials
                           :accounts {:data mock/mock-aws-accounts
                                      :selected #{}}
                           :resources {:data mock/mock-aws-resources
                                       :selected #{}
                                       :errors #{"rds-mysql-staging"}
                                       :status nil}
                           :agents {:data mock/mock-agents
                                    :assignments mock/mock-agent-assignments}})))

;; Setup type event
(rf/reg-event-db
 :connection-setup/set-type
 (fn [db [_ type]]
   (-> db
       (assoc-in [:connection-setup :type] type)
       (assoc-in [:aws-connect :current-step] :credentials))))

;; Navigation events
(rf/reg-event-db
 :aws-connect/set-current-step
 (fn [db [_ step]]
   (assoc-in db [:aws-connect :current-step] step)))

;; Credentials events
(rf/reg-event-db
 :aws-connect/set-credentials-type
 (fn [db [_ type]]
   (-> db
       (assoc-in [:aws-connect :credentials :type] type)
       (assoc-in [:aws-connect :credentials :iam-role] nil)
       (assoc-in [:aws-connect :credentials :iam-user] {:access-key-id nil
                                                        :secret-access-key nil
                                                        :region nil
                                                        :session-token nil}))))

(rf/reg-event-db
 :aws-connect/set-iam-role
 (fn [db [_ role]]
   (assoc-in db [:aws-connect :credentials :iam-role] role)))

(rf/reg-event-db
 :aws-connect/set-iam-user-credentials
 (fn [db [_ field value]]
   (assoc-in db [:aws-connect :credentials :iam-user field] value)))

;; Account selection events
(rf/reg-event-fx
 :aws-connect/set-selected-accounts
 (fn [{:keys [db]} [_ accounts]]
   {:db (-> db
            (assoc-in [:aws-connect :accounts :selected] (set accounts)))
    :dispatch [:aws-connect/fetch-resources]}))

;; Resource selection events
(rf/reg-event-db
 :aws-connect/set-selected-resources
 (fn [db [_ resources]]
   (assoc-in db [:aws-connect :resources :selected] (set resources))))

;; Agent assignment events
(rf/reg-event-db
 :aws-connect/set-agent-assignment
 (fn [db [_ resource agent]]
   (assoc-in db [:aws-connect :agents :assignments resource] agent)))

;; Mock data effects
(rf/reg-event-fx
 :aws-connect/validate-credentials
 (fn [{:keys [db]} _]
   {:db (-> db
            (assoc-in [:aws-connect :status] :validating)
            (assoc-in [:aws-connect :accounts :data] []))
    :dispatch-later [{:ms 1000
                      :dispatch [:aws-connect/validate-credentials-success
                                 {:accounts mock/mock-aws-accounts}]}]}))

(rf/reg-event-fx
 :aws-connect/validate-credentials-success
 (fn [{:keys [db]} [_ response]]
   {:db (-> db
            (assoc-in [:aws-connect :status] :credentials-valid)
            (assoc-in [:aws-connect :accounts :data] (:accounts response))
            (assoc-in [:aws-connect :current-step] :accounts))}))

(rf/reg-event-fx
 :aws-connect/validate-credentials-failure
 (fn [{:keys [db]} [_ response]]
   {:db (-> db
            (assoc-in [:aws-connect :status] :credentials-invalid)
            (assoc-in [:aws-connect :error] (:error response)))}))

(rf/reg-event-fx
 :aws-connect/fetch-resources
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:aws-connect :resources :status] :loading)
    :dispatch-later [{:ms 1000
                      :dispatch [:aws-connect/fetch-resources-success
                                 {:resources mock/mock-aws-resources}]}]}))

(rf/reg-event-fx
 :aws-connect/fetch-resources-success
 (fn [{:keys [db]} [_ response]]
   {:db (-> db
            (assoc-in [:aws-connect :resources :status] :loaded)
            (assoc-in [:aws-connect :resources :data] (:resources response)))}))

(rf/reg-event-fx
 :aws-connect/fetch-resources-failure
 (fn [{:keys [db]} [_ response]]
   {:db (-> db
            (assoc-in [:aws-connect :resources :status] :error)
            (assoc-in [:aws-connect :resources :error] (:error response)))}))

(rf/reg-event-fx
 :aws-connect/create-connections
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:aws-connect :status] :creating)
    :dispatch-later [{:ms 2000
                      :dispatch [:aws-connect/create-connections-success]}]}))

(rf/reg-event-fx
 :aws-connect/create-connections-success
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:aws-connect :status] :completed)
    :dispatch [:navigate :connections]}))

(rf/reg-event-fx
 :aws-connect/create-connections-failure
 (fn [{:keys [db]} [_ response]]
   {:db (-> db
            (assoc-in [:aws-connect :status] :error)
            (assoc-in [:aws-connect :error] (:error response)))}))
