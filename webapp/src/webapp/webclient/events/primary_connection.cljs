(ns webapp.webclient.events.primary-connection
  (:require
   [clojure.edn :refer [read-string]]
   [re-frame.core :as rf]))

;; Events
(rf/reg-event-fx
 :primary-connection/initialize-from-query-or-persistence
 (fn [_ _]
   (let [search-string (.. js/window -location -search)
         url-params (new js/URLSearchParams search-string)
         role-from-query (.get url-params "role")]
     (if (and role-from-query (not= role-from-query ""))
       {:fx [[:dispatch [:connections->get-connection-details
                         role-from-query
                         [:primary-connection/set-from-details]]]]}
       {:fx [[:dispatch [:primary-connection/load-persisted]]]}))))

(rf/reg-event-fx
 :primary-connection/initialize-with-persistence
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:editor :connections :status] :loading)
    :fx [[:dispatch-later {:ms 500 :dispatch [:primary-connection/initialize-from-query-or-persistence]}]]}))

(rf/reg-event-db
 :primary-connection/set-filter
 (fn [db [_ filter-text]]
   (assoc-in db [:editor :connections :filter] filter-text)))

(rf/reg-event-fx
 :primary-connection/set-selected
 (fn [{:keys [db]} [_ new-primary]]
   (let [current-multiples (get-in db [:editor :multi-connections :selected] [])
         compatible-multiples (filter #(and (= (:type %) (:type new-primary))
                                            (= (:subtype %) (:subtype new-primary))
                                            (not= (:name %) (:name new-primary)))
                                      current-multiples)]
     {:db (update-in db [:editor]
                     merge
                     {:connections {:selected new-primary}
                      :execution-requirements-callout {:dismissed? false}
                      :multi-connections {:selected compatible-multiples}})
      :fx [[:dispatch [:editor-plugin/clear-language]]
           [:dispatch [:primary-connection/persist-selected]]
           [:dispatch [:primary-connection/update-url-with-role (:name new-primary)]]
           [:dispatch [:database-schema->clear-schema]]
           [:dispatch [:ai-session-analyzer/get-role-rule (:name new-primary)]]]})))

(rf/reg-event-fx
 :primary-connection/persist-selected
 (fn [{:keys [db]} _]
   (let [selected (get-in db [:editor :connections :selected])]
     (.setItem js/localStorage
               "selected-connection"
               (when selected (pr-str {:name (:name selected)})))
     {})))

(rf/reg-event-fx
 :primary-connection/load-persisted
 (fn [_ _]
   (let [saved (.getItem js/localStorage "selected-connection")
         parsed (when (and saved (not= saved "null"))
                  (read-string saved))
         connection-name (:name parsed)]

     (if connection-name
       {:fx [[:dispatch [:connections->get-connection-details
                         connection-name
                         [:primary-connection/set-from-details]]]]}
       {}))))


(rf/reg-event-fx
 :primary-connection/clear-selected
 (fn [{:keys [db]} _]
   (.removeItem js/localStorage "selected-connection")
   {:db (-> db
            (assoc-in [:editor :connections :selected] nil)
            (assoc-in [:editor :execution-requirements-callout :dismissed?] false))
    :fx [[:dispatch [:ai-session-analyzer/clear-role-rule]]]}))

(rf/reg-event-db
 :primary-connection/dismiss-execution-requirements-callout
 (fn [db _]
   (assoc-in db [:editor :execution-requirements-callout :dismissed?] true)))

;; Set primary connection from loaded details
(rf/reg-event-fx
 :primary-connection/set-from-details
 (fn [{:keys [db]} [_ connection-name]]
   (let [connection (get-in db [:connections :details connection-name])
         enabled? (and connection
                       (not= "disabled" (:access_mode_exec connection)))]
     (if enabled?
       {:db (assoc-in db [:editor :connections :selected] connection)
        :fx [[:dispatch [:ai-session-analyzer/get-role-rule (or (:name connection) (:id connection))]]
             [:dispatch [:primary-connection/update-url-with-role (or (:name connection) (:id connection))]]]}
       {:db (assoc-in db [:editor :connections :selected] nil)
        :fx [[:dispatch [:primary-connection/clear-selected]]]}))))

(rf/reg-event-fx
 :primary-connection/update-url-with-role
 (fn [_ [_ role-name]]
   (when role-name
     (let [current-url (.. js/window -location -href)
           url (js/URL. current-url)
           search-params (.-searchParams url)]
       (.set search-params "role" role-name)
       (set! (.-search url) (.toString search-params))
       (.replaceState js/history nil "" (.toString url))))
   {}))

;; Dialog Events (for compact UI)
(rf/reg-event-db
 :primary-connection/toggle-dialog
 (fn [db [_ open?]]
   (assoc-in db [:editor :connections :dialog-open?] open?)))


(rf/reg-sub
 :primary-connection/selected
 (fn [db]
   (get-in db [:editor :connections :selected])))



(rf/reg-sub
 :primary-connection/dialog-open?
 (fn [db]
   (get-in db [:editor :connections :dialog-open?])))

(rf/reg-sub
 :primary-connection/execution-requirements-callout-dismissed?
 (fn [db]
   (get-in db [:editor :execution-requirements-callout :dismissed?] false)))
