(ns webapp.events.reports
  (:require [clojure.string :as cs]
            [re-frame.core :as rf]))

;; Only the per-session report survives here: the dashboard moved to webapp_v2
;; (see webapp_v2/src/pages/Dashboard). These events back the session-detail and
;; data-masking-analytics views in webapp.audit.

(rf/reg-event-fx
 :reports->get-report-by-session-id
 (fn
   [{:keys [db]} [_ session]]
   {:db (assoc db :reports->session {:status :loading
                                     :data nil})
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri (str "/reports/sessions?id=" (:id session) "&start_date="
                                (first (cs/split (:start_date session) #"T")))
                      :on-success #(rf/dispatch [::reports->set-report-by-session-id %])}]]]}))

(rf/reg-event-fx
 :reports->clear-session-report-by-id
 (fn [{:keys [db]} [_]]
   {:db (assoc db :reports->session {:status :loading
                                     :data nil})}))

(rf/reg-event-fx
 ::reports->set-report-by-session-id
 (fn
   [{:keys [db]} [_ report]]
   {:db (assoc db :reports->session {:status :ready
                                     :data report})}))
