(ns webapp.events.jobs
  (:require [re-frame.core :as rf]))

(rf/reg-event-fx
 :jobs/fetch-aws-connect-jobs
 (fn [{:keys [db]} _]
   {:dispatch [:fetch
               {:method "GET"
                :uri "/dbroles/jobs"
                :on-success #(rf/dispatch [:jobs/fetch-aws-connect-jobs-success %])
                :on-failure #(rf/dispatch [:jobs/fetch-aws-connect-jobs-failure %])}]}))

(rf/reg-event-fx
 :jobs/fetch-aws-connect-jobs-success
 (fn [{:keys [db]} [_ response]]
   (let [jobs (or (:items response) (:data response) [])
         has-running-jobs? (some (fn [job]
                                   (or (= (or (:phase job) (:status job)) "running")
                                       (nil? (:completed_at job))))
                                 jobs)]
     (if has-running-jobs?
       {:db (-> db
                (assoc-in [:jobs :aws-connect] jobs)
                (assoc-in [:jobs :has-running-jobs?] true))}

       {:db (-> db
                (assoc-in [:jobs :aws-connect] jobs)
                (assoc-in [:jobs :has-running-jobs?] false)
                (assoc-in [:jobs :polling-active?] false))
        :dispatch-n [[:jobs/stop-aws-connect-polling]
                     [:connections->get-connections {:force-refresh? true}]]}))))

(rf/reg-event-db
 :jobs/fetch-aws-connect-jobs-failure
 (fn [db [_ _]]
   (-> db
       (assoc-in [:jobs :aws-connect] [])
       (assoc-in [:jobs :has-running-jobs?] false))))

(rf/reg-event-fx
 :jobs/continue-aws-connect-polling
 (fn [{:keys [db]} _]
   (if (get-in db [:jobs :polling-active?])
     {:dispatch [:jobs/fetch-aws-connect-jobs]
      :dispatch-later [{:ms 5000
                        :dispatch [:jobs/continue-aws-connect-polling]}]}
     {:db db})))

(rf/reg-event-db
 :jobs/stop-aws-connect-polling
 (fn [db _]
   (assoc-in db [:jobs :polling-active?] false)))

(rf/reg-sub
 :jobs/aws-connect-running?
 (fn [db _]
   (get-in db [:jobs :has-running-jobs?] false)))

(rf/reg-sub
 :jobs/aws-connect-jobs
 (fn [db _]
   (get-in db [:jobs :aws-connect] [])))
