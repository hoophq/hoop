(ns webapp.events.agents
  (:require [re-frame.core :as rf]))

(rf/reg-event-fx
 :agents->get-agents
 (fn
   [{:keys [db]} [_]]
   {:db (assoc-in db [:agents :status] :loading)
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/agents"
                      :on-success #(rf/dispatch [::set-agents %])}]]]}))
(rf/reg-event-fx
 ::set-agents
 (fn
   [{:keys [db]} [_ agents]]
   {:db (assoc db :agents {:status :ready :data agents})}))

(rf/reg-event-fx
 :agents->generate-agent-key
 (fn [{:keys [db]} [_ agent-name]]
   {:fx [[:dispatch
          [:fetch
           {:method "POST"
            :uri "/agents"
            :body {:name agent-name}
            :on-success #(rf/dispatch [:agents->set-agent-key % :ready])
            :on-failure (fn [error]
                          (rf/dispatch [:agents->set-agent-key {} :error])
                          (rf/dispatch [:show-snackbar {:level :error
                                                        :text (:message error)
                                                        :details error}]))}]]]
    :db (assoc db :agents->agent-key {:status :loading :data {}})}))

(rf/reg-event-fx
 :agents->set-agent-key
 (fn [{:keys [db]} [_ agent status]]
   {:db (assoc db :agents->agent-key {:status status
                                      :data agent})}))

(rf/reg-event-fx
 :agents->delete-agent
 (fn [{:keys [db]} [_ agent-id]]
   {:fx [[:dispatch
          [:fetch
           {:method "DELETE"
            :uri (str "/agents/" agent-id)
            :on-success (fn []
                          (rf/dispatch [:show-snackbar {:level :success
                                                        :text "Agent deleted successfully!"}])
                          (rf/dispatch [:agents->get-agents]))
            :on-failure (fn [error]
                          (rf/dispatch [:show-snackbar {:level :error
                                                        :text "Failed to delete agent"
                                                        :details error}]))}]]]}))

(rf/reg-sub
 :agents->agent-key
 (fn [db _]
   (:agents->agent-key db)))
