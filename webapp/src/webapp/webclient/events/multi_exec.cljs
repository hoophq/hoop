(ns webapp.webclient.events.multi-exec
  (:require
   [re-frame.core :as rf]
   [clojure.string :as cs]))

;; ---- Multi Script Execution Events ----

(rf/reg-event-fx
 :multi-exec/execute-script
 (fn [{:keys [db]} [_ exec-list]]
   (let [on-failure (fn [error exec]
                      (rf/dispatch [:multi-exec/script-failure error exec]))
         on-success (fn [res exec]
                      (rf/dispatch [:multi-exec/script-success res exec]))
         dispatches (mapv (fn [exec]
                            [:dispatch-later
                             {:ms 1000
                              :dispatch [:fetch
                                         {:method "POST"
                                          :uri "/sessions"
                                          :on-success #(on-success % exec)
                                          :on-failure #(on-failure % exec)
                                          :body {:script (:script exec)
                                                 :connection (:connection-name exec)
                                                 :metadata (:metadata exec)}}]}])
                          exec-list)]
     {:db (assoc db :multi-exec {:data exec-list
                                 :status :running
                                 :type :script})
      :fx dispatches})))

(rf/reg-event-fx
 :multi-exec/script-success
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
 :multi-exec/script-failure
 (fn [{:keys [db]} [_ error current-exec]]
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

;; ---- Multi Runbook Execution Events ----

(rf/reg-event-fx
 :multi-exec/execute-runbook
 (fn [{:keys [db]} [_ runbook-list]]
   (let [on-failure (fn [error exec]
                      (rf/dispatch [:multi-exec/runbook-failure error exec]))
         on-success (fn [res exec]
                      (rf/dispatch [:multi-exec/runbook-success res exec]))
         dispatches (mapv (fn [runbook]
                            [:dispatch-later
                             {:ms 1000
                              :dispatch [:fetch
                                         {:method "POST"
                                          :uri (str "/plugins/runbooks/connections/"
                                                    (:connection-name runbook)
                                                    "/exec")
                                          :on-success #(on-success % runbook)
                                          :on-failure #(on-failure % runbook)
                                          :body {:file_name (:file_name runbook)
                                                 :parameters (:parameters runbook)}}]}])
                          runbook-list)]
     {:db (assoc db :multi-exec {:data runbook-list
                                 :status :running
                                 :type :runbook})
      :fx dispatches})))

(rf/reg-event-fx
 :multi-exec/runbook-success
 (fn [{:keys [db]} [_ result current-runbook]]
   (let [current-runbook-parsed {:connection-name (:connection-name current-runbook)
                                 :type (:type current-runbook)
                                 :subtype (:subtype current-runbook)
                                 :session-id (:session_id result)
                                 :status (if (:has_review result)
                                           :waiting-review
                                           :completed)}
         executions (:data (:multi-exec db))
         updated-executions (mapv (fn [exec]
                                    (if (= (:connection-name exec)
                                           (:connection-name current-runbook))
                                      current-runbook-parsed
                                      exec))
                                  executions)
         all-finished? (every? #(contains? #{:completed :waiting-review :error}
                                           (:status %))
                               updated-executions)]
     {:db (assoc db :multi-exec {:data updated-executions
                                 :status (if all-finished? :completed :running)
                                 :type :runbook})})))

(rf/reg-event-fx
 :multi-exec/runbook-failure
 (fn [{:keys [db]} [_ error current-runbook]]
   (let [current-runbook-parsed {:connection-name (:connection-name current-runbook)
                                 :type (:type current-runbook)
                                 :subtype (:subtype current-runbook)
                                 :status :error}
         executions (:data (:multi-exec db))
         updated-executions (mapv (fn [exec]
                                    (if (= (:connection-name exec)
                                           (:connection-name current-runbook))
                                      current-runbook-parsed
                                      exec))
                                  executions)
         all-finished? (every? #(contains? #{:completed :waiting-review :error}
                                           (:status %))
                               updated-executions)]
     {:db (assoc db :multi-exec {:data updated-executions
                                 :status (if all-finished? :completed :running)
                                 :type :runbook})})))

;; ---- Common Events ----

(rf/reg-event-fx
 :multi-exec/clear
 (fn [{:keys [db]} _]
   {:db (assoc db :multi-exec {:data [] :status :ready :type nil})}))

(rf/reg-event-fx
 :multi-exec/update-metadata
 (fn [{:keys [db]} [_ exec-list]]
   (let [dispatches (mapv (fn [exec]
                            [:dispatch-later
                             {:ms 1000
                              :dispatch [:fetch
                                         {:method "PATCH"
                                          :uri (str "/sessions/" (:session-id exec) "/metadata")
                                          :body {:metadata
                                                 {"View related sessions"
                                                  (str (.. js/window -location -origin)
                                                       "/sessions/filtered?id="
                                                       (cs/join "," (mapv :session-id exec-list)))}}
                                          :on-failure #(js/console.error "Metadata update failed:" %)}]}])
                          exec-list)]
     {:fx dispatches})))

(rf/reg-event-fx
 :multi-exec/show-modal
 (fn [{:keys [db]} [_ executions]]
   {:db (assoc db :multi-exec {:data executions
                               :status :ready})}))

;; ---- Subscriptions ----

(rf/reg-sub
 :multi-exec/executions
 (fn [db]
   (get-in db [:multi-exec :data])))

(rf/reg-sub
 :multi-exec/status
 (fn [db]
   (get-in db [:multi-exec :status])))

(rf/reg-sub
 :multi-exec/type
 (fn [db]
   (get-in db [:multi-exec :type])))

(rf/reg-sub
 :multi-exec/modal
 (fn [db]
   (get db :multi-exec)))
