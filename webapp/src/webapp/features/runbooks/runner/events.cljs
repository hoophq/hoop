(ns webapp.features.runbooks.runner.events
  (:require
   [clojure.edn :refer [read-string]]
   [re-frame.core :as rf]))

;; Connection Events
(rf/reg-fx
 :runbooks/fetch-connections
 (fn [_]
   (rf/dispatch [:fetch
                 {:method "GET"
                  :uri "/connections"
                  :on-success #(rf/dispatch [:runbooks/set-connections-list %])
                  :on-failure #(rf/dispatch [:runbooks/set-connections-error %])}])))

(rf/reg-event-fx
 :runbooks/initialize-connections
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:runbooks :connections :status] :loading)
    :fx [[:runbooks/fetch-connections]]}))

(rf/reg-event-db
 :runbooks/set-connections-error
 (fn [db [_ error]]
   (-> db
       (assoc-in [:runbooks :connections :status] :error)
       (assoc-in [:runbooks :connections :error] error))))

(rf/reg-event-fx
 :runbooks/set-connections-list
 (fn [{:keys [db]} [_ connections]]
   (let [selected (get-in db [:runbooks :selected-connection])
         updated-selected (when (and selected (:name selected))
                            (first (filter #(= (:name %) (:name selected)) connections)))]
     {:db (-> db
              (assoc-in [:runbooks :connections :status] :success)
              (assoc-in [:runbooks :connections :list] connections)
              (cond-> updated-selected
                (assoc-in [:runbooks :selected-connection] updated-selected)))
      :fx (concat
           (when updated-selected
             [[:dispatch [:runbooks/update-runbooks-for-connection]]])
           [[:dispatch [:runbooks/load-persisted-connection]]])})))

(rf/reg-event-db
 :runbooks/set-connection-filter
 (fn [db [_ filter-text]]
   (assoc-in db [:runbooks :connections :filter] filter-text)))

(rf/reg-event-fx
 :runbooks/set-selected-connection
 (fn [{:keys [db]} [_ connection]]
   {:db (assoc-in db [:runbooks :selected-connection] connection)
    :fx [[:dispatch [:runbooks/persist-selected-connection]]
         [:dispatch [:runbooks/update-runbooks-for-connection]]]}))

(rf/reg-event-fx
 :runbooks/clear-selected-connection
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:runbooks :selected-connection] nil)
    :fx [[:dispatch [:runbooks/persist-selected-connection]]
         [:dispatch [:runbooks/update-runbooks-for-connection]]]}))

(rf/reg-event-fx
 :runbooks/persist-selected-connection
 (fn [{:keys [db]} _]
   (let [selected (get-in db [:runbooks :selected-connection])]
     (.setItem js/localStorage
               "runbooks-selected-connection"
               (when selected (pr-str {:name (:name selected)})))
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
       {:db (assoc-in db [:runbooks :selected-connection] parsed)}
       
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
   (assoc db :runbooks/execute-trigger false)))
