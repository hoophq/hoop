(ns webapp.events.audit-logs
  (:require
   [clojure.string :as string]
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :audit-logs/fetch
 (fn
   [{:keys [db]} [_ {:keys [page page-size append?]}]]
   (let [filters (get-in db [:audit-logs :filters])
         page (or page (get-in db [:audit-logs :pagination :page]) 1)
         page-size (or page-size (get-in db [:audit-logs :pagination :size]) 25)
         query-params (cond-> {"page" page
                               "page_size" page-size}
                        (:actor-email filters)
                        (assoc "actor_email" (:actor-email filters))

                        (:created-after filters)
                        (assoc "created_after" (:created-after filters))

                        (:created-before filters)
                        (assoc "created_before" (:created-before filters)))
         success-event (if append? :audit-logs/append-data :audit-logs/set-data)]
     {:db (assoc-in db [:audit-logs :status] :loading)
      :fx [[:dispatch [:fetch {:method "GET"
                               :uri "/audit/logs"
                               :query-params query-params
                               :on-success #(rf/dispatch [success-event %])
                               :on-failure #(rf/dispatch [:audit-logs/set-error %])}]]]})))

(rf/reg-event-db
 :audit-logs/set-data
 (fn
   [db [_ response]]
   (-> db
       (assoc-in [:audit-logs :status] :success)
       (assoc-in [:audit-logs :data] (:data response))
       (assoc-in [:audit-logs :pagination] (merge
                                            (get-in db [:audit-logs :pagination])
                                            {:total (get-in response [:pages :total])
                                             :page (get-in response [:pages :page])
                                             :size (get-in response [:pages :size])
                                             :has-more? (< (* (get-in response [:pages :page])
                                                              (get-in response [:pages :size]))
                                                           (get-in response [:pages :total]))})))))

(rf/reg-event-fx
 :audit-logs/load-more
 (fn
   [{:keys [db]} [_]]
   (let [pagination (get-in db [:audit-logs :pagination])
         current-page (:page pagination)
         has-more? (:has-more? pagination)
         loading? (= :loading (get-in db [:audit-logs :status]))]
     (if (and has-more? (not loading?))
       {:db (assoc-in db [:audit-logs :status] :loading)
        :fx [[:dispatch [:audit-logs/fetch {:page (inc current-page) :append? true}]]]}
       {}))))

(rf/reg-event-db
 :audit-logs/append-data
 (fn
   [db [_ response]]
   (-> db
       (assoc-in [:audit-logs :status] :success)
       (update-in [:audit-logs :data] concat (:data response))
       (assoc-in [:audit-logs :pagination] (merge
                                            (get-in db [:audit-logs :pagination])
                                            {:total (get-in response [:pages :total])
                                             :page (get-in response [:pages :page])
                                             :size (get-in response [:pages :size])
                                             :has-more? (< (* (get-in response [:pages :page])
                                                              (get-in response [:pages :size]))
                                                           (get-in response [:pages :total]))})))))

(rf/reg-event-db
 :audit-logs/set-error
 (fn
   [db [_ error]]
   (-> db
       (assoc-in [:audit-logs :status] :error)
       (assoc-in [:audit-logs :error] error))))

(rf/reg-event-fx
 :audit-logs/set-filters
 (fn
   [{:keys [db]} [_ new-filters]]
   (let [current-filters (get-in db [:audit-logs :filters])
         updated-filters (merge current-filters new-filters)]
     {:db (-> db
              (assoc-in [:audit-logs :filters] updated-filters)
              (assoc-in [:audit-logs :pagination :page] 1))
      :fx [[:dispatch [:audit-logs/fetch {}]]]})))

(rf/reg-event-fx
 :audit-logs/set-page
 (fn
   [{:keys [db]} [_ page]]
   {:db (assoc-in db [:audit-logs :pagination :page] page)
    :fx [[:dispatch [:audit-logs/fetch {:page page}]]]}))

(rf/reg-event-db
 :audit-logs/toggle-expand
 (fn
   [db [_ log-id]]
   (let [expanded-rows (get-in db [:audit-logs :expanded-rows])
         new-expanded-rows (if (contains? expanded-rows log-id)
                             (disj expanded-rows log-id)
                             (conj expanded-rows log-id))]
     (assoc-in db [:audit-logs :expanded-rows] new-expanded-rows))))

(rf/reg-event-fx
 :audit-logs/export
 (fn
   [{:keys [db]} [_]]
   (let [data (get-in db [:audit-logs :data])
         csv-header "Timestamp,User,Email,Operation,Resource Type,Resource Name,Outcome,Error Message\n"
         csv-rows (map (fn [log]
                         (let [timestamp (:created_at log)
                               user (or (:actor_name log) "Unknown")
                               email (or (:actor_email log) "")
                               operation (:action log)
                               resource-type (:resource_type log)
                               resource-name (or (:resource_name log) "")
                               outcome (if (:outcome log) "Success" "Failure")
                               error-msg (or (:error_message log) "")]
                           (string/join "," [timestamp user email operation resource-type resource-name outcome error-msg])))
                       data)
         csv-content (str csv-header (string/join "\n" csv-rows))
         blob (js/Blob. #js[csv-content] #js{:type "text/csv"})
         url (js/URL.createObjectURL blob)
         link (js/document.createElement "a")]
     (.setAttribute link "href" url)
     (.setAttribute link "download" "audit-logs.csv")
     (.click link)
     (js/URL.revokeObjectURL url)
     {:fx [[:dispatch [:show-snackbar {:level :success
                                       :text "Audit logs exported successfully"}]]]})))
