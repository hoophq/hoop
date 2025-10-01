(ns webapp.features.runbooks.runner.events
  (:require
   [clojure.edn :refer [read-string]]
   [re-frame.core :as rf]))

(rf/reg-event-db
 :runbooks/set-active-runbook
 (fn
   [db [_ template]]
   (assoc db :runbooks-plugin->selected-runbooks {:status :ready
                                                  :data {:name (:name template)
                                                         :error (:error template)
                                                         :params (keys (:metadata template))
                                                         :file_url (:file_url template)
                                                         :metadata (:metadata template)
                                                         :connections (:connections template)}})))

(rf/reg-event-db
 :runbooks/set-active-runbook-by-name
 (fn
   [db [_ runbook-name]]
   (let [runbooks (get-in db [:runbooks-plugin->runbooks :data])
         runbook  (some (fn [r] (when (= (:name r) runbook-name) r)) runbooks)]
     (if runbook
       (assoc db :runbooks-plugin->selected-runbooks
              {:status :ready
               :data {:name        (:name runbook)
                      :error       (:error runbook)
                      :params      (keys (:metadata runbook))
                      :file_url    (:file_url runbook)
                      :metadata    (:metadata runbook)
                      :connections (:connections runbook)}})
       (assoc db :runbooks-plugin->selected-runbooks {:status :error :data nil})))))

;; Connection Events
(rf/reg-event-fx
 :runbooks/set-selected-connection
 (fn [{:keys [db]} [_ connection]]
   {:db (assoc-in db [:runbooks :selected-connection] connection)
    :fx [[:dispatch [:runbooks/persist-selected-connection]]
         [:dispatch [:runbooks/update-runbooks-for-connection]]]}))

(rf/reg-event-fx
 :runbooks/persist-selected-connection
 (fn [{:keys [db]} _]
   (let [selected (get-in db [:runbooks :selected-connection])]
     (.setItem js/localStorage
               "runbooks-selected-connection"
               (when selected (pr-str selected)))
     {})))

(rf/reg-event-fx
 :runbooks/load-persisted-connection
 (fn [{:keys [db]} _]
   (let [saved (.getItem js/localStorage "runbooks-selected-connection")
         parsed (when (and saved (not= saved "null"))
                  (read-string saved))
         connection-name (:name parsed)
         connections (get-in db [:runbooks :connections :list])
         updated-connection (when (and connection-name connections)
                              (first (filter #(= (:name %) connection-name) connections)))]
     (cond
       updated-connection
       {:db (assoc-in db [:runbooks :selected-connection] updated-connection)
        :fx [[:dispatch [:runbooks/update-runbooks-for-connection]]]}
       
       (and parsed (empty? connections))
       {:db (assoc-in db [:runbooks :selected-connection] parsed)
        :fx [[:dispatch [:runbooks/update-runbooks-for-connection]]]}
       
       (and parsed (seq connections) (not updated-connection))
       {:db (assoc-in db [:runbooks :selected-connection] nil)
        :fx [[:dispatch [:runbooks/persist-selected-connection]]]}
       
       :else {}))))

(rf/reg-event-fx
 :runbooks/update-runbooks-for-connection
 (fn [{:keys [db]} _]
   (let [selected-connection (get-in db [:runbooks :selected-connection])]
     {:fx [[:dispatch [:runbooks-plugin->get-runbooks
                       (when selected-connection [(:name selected-connection)])]]]})))

;; Dialog Events
(rf/reg-event-db
 :runbooks/toggle-connection-dialog
 (fn [db [_ open?]]
   (assoc-in db [:runbooks :connection-dialog-open?] open?)))

;; Execution Events
(rf/reg-event-db
 :runbooks/trigger-execute
 (fn [db _]
   (assoc-in db [:runbooks :execute-trigger] true)))

(rf/reg-event-db
 :runbooks/execute-handled
 (fn [db _]
   (assoc-in db [:runbooks :execute-trigger] false)))
