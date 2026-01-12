(ns webapp.parallel-mode.events.execution
  (:require
   [re-frame.core :as rf]))

;; ---- Parallel Execution Events ----

(rf/reg-event-fx
 :parallel-mode/execute-immediately
 (fn [{:keys [db]} [_ all-exec-list to-execute-list]]
   (js/console.log "🚀 execute-immediately called" 
                   "total:" (count all-exec-list)
                   "to-execute:" (count to-execute-list))
   ;; Show modal with all items (including pre-failed)
   ;; Then execute only valid ones
   {:db (assoc db :multi-exec {:data all-exec-list
                               :status :running
                               :type :script})
    :fx [[:dispatch [:parallel-mode/execute-script to-execute-list]]]}))

(rf/reg-event-fx
 :parallel-mode/execute-script
 (fn [{:keys [db]} [_ exec-list]]
   (let [on-failure (fn [error exec]
                      (rf/dispatch [:parallel-mode/script-failure error exec]))
         on-success (fn [res exec]
                      (rf/dispatch [:parallel-mode/script-success res exec]))
         ;; Mark items as running and dispatch requests
         updated-list (map #(assoc % :status :running) exec-list)
         dispatches (mapv (fn [exec]
                            [:dispatch-later
                             {:ms 100
                              :dispatch [:fetch
                                         {:method "POST"
                                          :uri "/sessions"
                                          :on-success #(on-success % exec)
                                          :on-failure #(on-failure % exec)
                                          :body {:script (:script exec)
                                                 :connection (:connection-name exec)
                                                 :metadata (:metadata exec)
                                                 :env_vars (:env_vars exec)}}]}])
                          exec-list)]
     ;; Update status to running for items being executed
     {:db (update-in db [:multi-exec :data]
                     (fn [all-data]
                       (mapv (fn [item]
                               (if (some #(= (:connection-name %) (:connection-name item)) updated-list)
                                 (assoc item :status :running)
                                 item))
                             all-data)))
      :fx dispatches})))

(rf/reg-event-fx
 :parallel-mode/script-success
 (fn [{:keys [db]} [_ result current-exec]]
   (let [current-exec-parsed {:connection-name (:connection-name current-exec)
                              :type (:type current-exec)
                              :subtype (:subtype current-exec)
                              :session-id (:session_id result)
                              :status (if (:has_review result)
                                        :waiting-review
                                        :completed)}
         executions (:data (:multi-exec db))
         updated-executions (mapv (fn [exec]
                                    (if (= (:connection-name exec)
                                           (:connection-name current-exec))
                                      (merge exec current-exec-parsed)
                                      exec))
                                  executions)
         all-finished? (every? #(contains? #{:completed :waiting-review :error :error-jira-template 
                                             :error-metadata-required :cancelled}
                                           (:status %))
                               updated-executions)]
     {:db (assoc db :multi-exec {:data updated-executions
                                 :status (if all-finished? :completed :running)
                                 :type :script})
      :fx (when all-finished?
            [[:dispatch [:parallel-mode/schedule-auto-close]]])})))

(rf/reg-event-fx
 :parallel-mode/script-failure
 (fn [{:keys [db]} [_ error current-exec]]
   (let [current-exec-parsed {:connection-name (:connection-name current-exec)
                              :type (:type current-exec)
                              :subtype (:subtype current-exec)
                              :status :error
                              :error-message (or (:message error) "Request failed")}
         executions (:data (:multi-exec db))
         updated-executions (mapv (fn [exec]
                                    (if (= (:connection-name exec)
                                           (:connection-name current-exec))
                                      (merge exec current-exec-parsed)
                                      exec))
                                  executions)
         all-finished? (every? #(contains? #{:completed :waiting-review :error :error-jira-template 
                                             :error-metadata-required :cancelled}
                                           (:status %))
                               updated-executions)]
     {:db (assoc db :multi-exec {:data updated-executions
                                 :status (if all-finished? :completed :running)
                                 :type :script})
      :fx (when all-finished?
            [[:dispatch [:parallel-mode/schedule-auto-close]]])})))

(rf/reg-event-fx
 :parallel-mode/cancel-pending-executions
 (fn [{:keys [db]} _]
   (let [executions (:data (:multi-exec db))
         updated-executions (mapv (fn [exec]
                                    (if (= (:status exec) :queued)
                                      (assoc exec :status :cancelled)
                                      exec))
                                  executions)
         has-running? (some #(= (:status %) :running) updated-executions)]
     {:db (assoc-in db [:multi-exec :data] updated-executions)
      :fx (when-not has-running?
            [[:dispatch [:parallel-mode/clear-execution]]])})))

(rf/reg-event-fx
 :parallel-mode/schedule-auto-close
 (fn [_ _]
   {:fx [[:dispatch-later {:ms 3000
                           :dispatch [:parallel-mode/trigger-fade-out]}]]}))

(rf/reg-event-db
 :parallel-mode/trigger-fade-out
 (fn [db _]
   (assoc-in db [:multi-exec :fade-out?] true)))

(rf/reg-event-fx
 :parallel-mode/clear-execution
 (fn [{:keys [db]} _]
   {:db (assoc db :multi-exec {:data [] :status :ready :type nil :fade-out? false})}))

(rf/reg-event-fx
 :parallel-mode/show-execution-preview
 (fn [{:keys [db]} [_ executions]]
   (js/console.log "👀 show-execution-preview called" "executions:" (clj->js executions))
   ;; Use the same state as legacy multi-exec so the modal appears
   {:db (assoc db :multi-exec {:data executions
                               :status :ready
                               :type :script
                               :fade-out? false})}))

;; ---- Subscriptions ----

(rf/reg-sub
 :parallel-mode/execution-state
 (fn [db]
   (get db :multi-exec)))

(rf/reg-sub
 :parallel-mode/is-executing?
 :<- [:parallel-mode/execution-state]
 (fn [execution-state]
   (= (:status execution-state) :running)))

(rf/reg-sub
 :parallel-mode/should-fade-out?
 (fn [db]
   (get-in db [:multi-exec :fade-out?] false)))
