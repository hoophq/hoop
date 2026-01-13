(ns webapp.parallel-mode.subs
  (:require
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.parallel-mode.helpers :as helpers]))

;; ---- Modal Subscriptions ----

(rf/reg-sub
 :parallel-mode/modal-open?
 (fn [db _]
   (get-in db [:parallel-mode :modal :open?] false)))

(rf/reg-sub
 :parallel-mode/search-term
 (fn [db _]
   (get-in db [:parallel-mode :modal :search-term] "")))

;; ---- Selection Subscriptions ----

(rf/reg-sub
 :parallel-mode/selected-connections
 (fn [db _]
   (get-in db [:parallel-mode :selection :connections] [])))

(rf/reg-sub
 :parallel-mode/selected-count
 :<- [:parallel-mode/selected-connections]
 (fn [connections _]
   (count connections)))

(rf/reg-sub
 :parallel-mode/has-minimum?
 :<- [:parallel-mode/selected-connections]
 (fn [connections _]
   (helpers/has-minimum-connections? connections)))

(rf/reg-sub
 :parallel-mode/is-active?
 :<- [:parallel-mode/selected-connections]
 (fn [connections _]
   (helpers/has-minimum-connections? connections)))

;; ---- Connections Data Subscriptions ----

(rf/reg-sub
 :parallel-mode/valid-connections
 :<- [:connections->pagination]
 (fn [connections _]
   (let [all-connections (or (:data connections) [])]
     (helpers/filter-valid-connections all-connections))))

;; ---- Execution Summary Subscriptions ----

(rf/reg-sub
 :parallel-mode/execution-search-term
 (fn [db _]
   (get-in db [:parallel-mode :execution :search-term] "")))

(rf/reg-sub
 :parallel-mode/active-tab
 (fn [db _]
   (get-in db [:parallel-mode :execution :active-tab] "success")))

(rf/reg-sub
 :parallel-mode/all-executions
 :<- [:parallel-mode/execution-state]
 (fn [execution-state _]
   (:data execution-state [])))

(rf/reg-sub
 :parallel-mode/filtered-executions
 :<- [:parallel-mode/all-executions]
 :<- [:parallel-mode/execution-search-term]
 (fn [[executions search-term] _]
   (if (cs/blank? search-term)
     executions
     (let [lower-search (cs/lower-case search-term)]
       (filter (fn [exec]
                 (or (cs/includes? (cs/lower-case (:connection-name exec)) lower-search)
                     (cs/includes? (cs/lower-case (or (:type exec) "")) lower-search)
                     (cs/includes? (cs/lower-case (or (:subtype exec) "")) lower-search)))
               executions)))))

(rf/reg-sub
 :parallel-mode/running-executions
 :<- [:parallel-mode/filtered-executions]
 (fn [executions _]
   (filterv #(= (:status %) :running) executions)))

(rf/reg-sub
 :parallel-mode/success-executions
 :<- [:parallel-mode/filtered-executions]
 (fn [executions _]
   (filterv #(contains? #{:completed :waiting-review} (:status %)) executions)))

(rf/reg-sub
 :parallel-mode/error-executions
 :<- [:parallel-mode/filtered-executions]
 (fn [executions _]
   (filterv #(contains? #{:error :error-jira-template :error-metadata-required :cancelled}
                        (:status %))
            executions)))

(rf/reg-sub
 :parallel-mode/success-count
 :<- [:parallel-mode/success-executions]
 (fn [executions _]
   (count executions)))

(rf/reg-sub
 :parallel-mode/error-count
 :<- [:parallel-mode/error-executions]
 (fn [executions _]
   (count executions)))

(rf/reg-sub
 :parallel-mode/execution-progress
 :<- [:parallel-mode/all-executions]
 (fn [executions _]
   (let [total (count executions)
         completed (count (filter #(contains? #{:completed :waiting-review :error :error-jira-template
                                                :error-metadata-required :cancelled}
                                              (:status %))
                                  executions))
         running (count (filter #(= (:status %) :running) executions))
         percentage (if (> total 0)
                      (* (/ completed total) 100)
                      0)]
     {:total total
      :completed completed
      :running running
      :percentage percentage})))

;; ---- UI State ----


(rf/reg-sub
 :parallel-mode/batch-id
 (fn [db _]
   (get-in db [:multi-exec :batch-id])))
