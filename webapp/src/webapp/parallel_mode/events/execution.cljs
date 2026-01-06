(ns webapp.parallel-mode.events.execution
  (:require
   [re-frame.core :as rf]))

;; ---- Parallel Execution Events ----

(rf/reg-event-fx
 :parallel-mode/execute-script
 (fn [{:keys [db]} [_ exec-list]]
   (let [on-failure (fn [error exec]
                      (rf/dispatch [:parallel-mode/script-failure error exec]))
         on-success (fn [res exec]
                      (rf/dispatch [:parallel-mode/script-success res exec]))
         dispatches (mapv (fn [exec]
                            [:dispatch-later
                             {:ms 100  ; Reduzido de 1000ms
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
     ;; Use the same state as legacy multi-exec
     {:db (assoc db :multi-exec {:data exec-list
                                 :status :running
                                 :type :script})
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
                                      current-exec-parsed
                                      exec))
                                  executions)
         all-finished? (every? #(contains? #{:completed :waiting-review :error}
                                           (:status %))
                               updated-executions)]
     {:db (assoc db :multi-exec {:data updated-executions
                                 :status (if all-finished? :completed :running)
                                 :type :script})})))

(rf/reg-event-fx
 :parallel-mode/script-failure
 (fn [{:keys [db]} [_ _error current-exec]]
   (let [current-exec-parsed {:connection-name (:connection-name current-exec)
                              :type (:type current-exec)
                              :subtype (:subtype current-exec)
                              :status :error}
         executions (:data (:multi-exec db))
         updated-executions (mapv (fn [exec]
                                    (if (= (:connection-name exec)
                                           (:connection-name current-exec))
                                      current-exec-parsed
                                      exec))
                                  executions)
         all-finished? (every? #(contains? #{:completed :waiting-review :error}
                                           (:status %))
                               updated-executions)]
     {:db (assoc db :multi-exec {:data updated-executions
                                 :status (if all-finished? :completed :running)
                                 :type :script})})))

(rf/reg-event-fx
 :parallel-mode/clear-execution
 (fn [{:keys [db]} _]
   {:db (assoc db :multi-exec {:data [] :status :ready :type nil})}))

(rf/reg-event-fx
 :parallel-mode/show-execution-preview
 (fn [{:keys [db]} [_ executions]]
   ;; Use the same state as legacy multi-exec so the modal appears
   {:db (assoc db :multi-exec {:data executions
                               :status :ready
                               :type :script})}))

