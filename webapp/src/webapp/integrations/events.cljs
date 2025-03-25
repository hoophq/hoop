(ns webapp.integrations.events
  (:require [re-frame.core :as rf]))

;; Initialize the integrations state in the DB
(rf/reg-event-db
 :integrations/initialize
 (fn [db _]
   (assoc db :integrations {:aws-connect {:jobs []}})))

;; Add a new AWS Connect job to the jobs list
(rf/reg-event-db
 :integrations/add-aws-connect-job
 (fn [db [_ job]]
   (update-in db [:integrations :aws-connect :jobs] conj job)))

;; Update an existing AWS Connect job
(rf/reg-event-db
 :integrations/update-aws-connect-job
 (fn [db [_ job-id updates]]
   (update-in db [:integrations :aws-connect :jobs]
              (fn [jobs]
                (mapv (fn [job]
                        (if (= (:id job) job-id)
                          (merge job updates)
                          job))
                      jobs)))))

;; Load AWS Connect jobs from the backend (would connect to your backend API)
(rf/reg-event-fx
 :integrations/load-aws-connect-jobs
 (fn [{:keys [db]} _]
   ;; This would typically make an API call to fetch jobs
   ;; For now, we're just setting some mock data
   {:db (assoc-in db [:integrations :aws-connect :jobs]
                  [{:id "job-001"
                    :job-type "Create IAM Role"
                    :status :completed
                    :created-at "2023-03-15 14:30:25"
                    :message "IAM Role created successfully"}
                   {:id "job-002"
                    :job-type "Create S3 Bucket"
                    :status :running
                    :created-at "2023-03-15 14:31:10"
                    :message "Creating S3 bucket..."}
                   {:id "job-003"
                    :job-type "Setup CloudWatch Metrics"
                    :status :error
                    :created-at "2023-03-15 14:32:05"
                    :message "Failed to create CloudWatch alarm: insufficient permissions"}])}))
