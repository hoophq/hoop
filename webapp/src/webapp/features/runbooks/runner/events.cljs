(ns webapp.features.runbooks.runner.events
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-db
 :runbooks/set-active-runbook
 (fn
   [db [_ template repository]]
   (assoc db :runbooks-plugin->selected-runbooks {:status :ready
                                                  :data {:name (:name template)
                                                         :error (:error template)
                                                         :params (keys (:metadata template))
                                                         :file_url (:file_url template)
                                                         :metadata (:metadata template)
                                                         :connections (:connections template)
                                                         :repository repository}})))

(rf/reg-event-db
 :runbooks/set-active-runbook-by-name
 (fn
   [db [_ runbook-name]]
   (let [list-data (get-in db [:runbooks :list])
         repositories (:data list-data)
         all-items (mapcat :items (or repositories []))
         runbook  (some (fn [r] (when (= (:name r) runbook-name) r)) all-items)]
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
               (:name selected))
     {})))

(rf/reg-event-fx
 :runbooks/load-persisted-connection
 (fn [_ _]
   (let [saved (.getItem js/localStorage "runbooks-selected-connection")]
     ;; If old format (starts with "{"), clear it and start fresh
     (if (and saved (.startsWith saved "{"))
       (do
         (.removeItem js/localStorage "runbooks-selected-connection")
         {})
       ;; New format - fetch the connection
       (if (and saved (not= saved "null") (not= saved ""))
         {:fx [[:dispatch [:connections->get-connection-details
                           saved
                           [:runbooks/connection-loaded]]]]}
         {})))))

(rf/reg-event-fx
 :runbooks/connection-loaded
 (fn [{:keys [db]} [_ connection-name]]
   (let [connection (get-in db [:connections :details connection-name])]
     (if connection
       {:db (assoc-in db [:runbooks :selected-connection] connection)
        :fx [[:dispatch [:runbooks/update-runbooks-for-connection]]]}
       ;; Connection not found - clear selection and reload list without connection
       {:db (assoc-in db [:runbooks :selected-connection] nil)
        :fx [[:dispatch [:runbooks/persist-selected-connection]]
             [:dispatch [:runbooks/list nil]]]}))))

(rf/reg-event-fx
 :runbooks/update-runbooks-for-connection
 (fn [{:keys [db]} _]
   (let [selected-connection (get-in db [:runbooks :selected-connection])
         connection-name (when selected-connection (:name selected-connection))]
     {:fx [[:dispatch [:runbooks/list connection-name]]]})))

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
