(ns webapp.features.workflows.events
  (:require
   [re-frame.core :as rf]))

(def hard-cap
  "Maximum number of sessions we render in a single workflow timeline."
  200)

(def initial-state
  "Empty state for the :workflows db key. Reused by db init and reset events."
  {:correlation-id nil
   :status :idle
   :sessions []
   :total 0
   :truncated? false
   :expanded #{}
   :step-details {}
   :error nil})

(rf/reg-event-fx
 :workflows/get
 (fn
   [{:keys [db]} [_ correlation-id]]
   {:db (assoc db :workflows (assoc initial-state
                                    :correlation-id correlation-id
                                    :status :loading))
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/plugins/audit/sessions"
                             :query-params {"correlation_id" correlation-id
                                            "limit" hard-cap}
                             :on-success #(rf/dispatch [:workflows/set-data %])
                             :on-failure #(rf/dispatch [:workflows/set-error %])}]]]}))

(rf/reg-event-fx
 :workflows/set-data
 (fn
   [{:keys [db]} [_ response]]
   (let [data (or (:data response) [])
         total (or (:total response) (count data))
         sorted (vec (sort-by :start_date data))
         expanded-ids (into #{} (map :id) sorted)]
     {:db (-> db
              (assoc-in [:workflows :status] :ready)
              (assoc-in [:workflows :sessions] sorted)
              (assoc-in [:workflows :total] total)
              (assoc-in [:workflows :truncated?] (> total (count sorted)))
              (assoc-in [:workflows :expanded] expanded-ids))
      :fx (mapv (fn [session]
                  [:dispatch [:workflows/get-step-detail session]])
                sorted)})))

(rf/reg-event-db
 :workflows/set-error
 (fn
   [db [_ error]]
   (-> db
       (assoc-in [:workflows :status] :error)
       (assoc-in [:workflows :error] error))))

(rf/reg-event-fx
 :workflows/toggle-step
 (fn
   [{:keys [db]} [_ session]]
   (let [session-id (:id session)
         expanded (get-in db [:workflows :expanded] #{})
         already-expanded? (contains? expanded session-id)
         already-loaded? (contains? (get-in db [:workflows :step-details] {}) session-id)
         next-expanded (if already-expanded?
                         (disj expanded session-id)
                         (conj expanded session-id))]
     (cond-> {:db (assoc-in db [:workflows :expanded] next-expanded)}
       (and (not already-expanded?) (not already-loaded?))
       (assoc :fx [[:dispatch [:workflows/get-step-detail session]]])))))

(rf/reg-event-fx
 :workflows/get-step-detail
 (fn
   [{:keys [db]} [_ session]]
   (let [session-id (:id session)]
     {:db (assoc-in db [:workflows :step-details session-id]
                    {:status :loading :data nil})
      :fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/sessions/" session-id "?expand=session_input")
                               :on-success #(rf/dispatch [:workflows/set-step-detail session-id %])
                               :on-failure #(rf/dispatch [:workflows/set-step-detail-error session-id %])}]]]})))

(rf/reg-event-db
 :workflows/set-step-detail
 (fn
   [db [_ session-id detail]]
   (assoc-in db [:workflows :step-details session-id]
             {:status :ready :data detail})))

(rf/reg-event-db
 :workflows/set-step-detail-error
 (fn
   [db [_ session-id error]]
   (assoc-in db [:workflows :step-details session-id]
             {:status :error :error error})))

(rf/reg-event-db
 :workflows/clear
 (fn
   [db [_]]
   (assoc db :workflows initial-state)))
