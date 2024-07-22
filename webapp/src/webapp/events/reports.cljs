(ns webapp.events.reports
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :reports->get-report-by-session-id
 (fn
   [{:keys [db]} [_ session-id]]
   {:db (assoc db :reports->session {:status :loading
                                     :data nil})
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri (str "/reports/sessions?id=" session-id)
                      :on-success #(rf/dispatch [::reports->set-session %])}]]]}))


(rf/reg-event-fx
 ::reports->set-session
 (fn
   [{:keys [db]} [_ report]]
   {:db (assoc db :reports->session {:status :ready
                                     :data report})}))


