(ns webapp.webclient.events.primary-connection
  (:require
   [clojure.edn :refer [read-string]]
   [clojure.string :as string]
   [re-frame.core :as rf]))

;; Events
(rf/reg-event-fx
 :primary-connection/initialize-with-persistence
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:editor :connections :status] :loading)
    :fx [[:dispatch-later {:ms 500 :dispatch [:primary-connection/load-persisted]}]
         [:dispatch-later {:ms 600 :dispatch [:multiple-connections/load-persisted]}]]}))

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
     {:db (-> db
              (assoc-in [:editor :connections :selected] new-primary)
              (assoc-in [:editor :multi-connections :selected] compatible-multiples))
      :fx [[:dispatch [:editor-plugin/clear-language]]
           [:dispatch [:primary-connection/persist-selected]]
           [:dispatch [:multiple-connections/persist]]
           [:dispatch [:database-schema->clear-schema]]]})))

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
 (fn [{:keys [db]} _]
   (let [saved (.getItem js/localStorage "selected-connection")
         parsed (when (and saved (not= saved "null"))
                  (read-string saved))
         connection-name (:name parsed)]

     (if connection-name
       {:fx [[:dispatch [:connections->get-connection-details
                         connection-name
                         [:primary-connection/set-from-details]]]]}
       {}))))

;; Set primary connection from loaded details
(rf/reg-event-fx
 :primary-connection/set-from-details
 (fn [{:keys [db]} [_ connection-name]]
   (let [connection (get-in db [:connections :details connection-name])]
     (if connection
       {:db (assoc-in db [:editor :connections :selected] connection)}

       {:db (assoc-in db [:editor :connections :selected] nil)}))))

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
