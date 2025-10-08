(ns webapp.webclient.events.primary-connection
  (:require
   [clojure.edn :refer [read-string]]
   [clojure.string :as string]
   [re-frame.core :as rf]))

(rf/reg-fx
 :fetch-connections
 (fn [_]
   (rf/dispatch [:connections->get-connections
                 {:on-success [:primary-connection/set-list]
                  :on-failure [:primary-connection/set-error]}])))

;; Events
(rf/reg-event-fx
 :primary-connection/initialize-with-persistence
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:editor :connections :status] :loading)
    :fx [[:fetch-connections]
         [:dispatch-later {:ms 500 :dispatch [:primary-connection/load-persisted]}]
         [:dispatch-later {:ms 600 :dispatch [:multiple-connections/load-persisted]}]]}))

(rf/reg-event-db
 :primary-connection/set-error
 (fn [db [_ error]]
   (-> db
       (assoc-in [:editor :connections :status] :error)
       (assoc-in [:editor :connections :error] error))))

(rf/reg-event-db
 :primary-connection/set-list
 (fn [db [_ connections]]
   (let [selected (get-in db [:editor :connections :selected])
         updated-selected (when (and selected (:name selected))
                            (first (filter #(= (:name %) (:name selected)) connections)))
         multi-selected (get-in db [:editor :multi-connections :selected])
         updated-multi-selected (when multi-selected
                                  (vec (keep (fn [saved-conn]
                                               (first (filter #(= (:name %) (:name saved-conn))
                                                              connections)))
                                             multi-selected)))]
     (-> db
         (assoc-in [:editor :connections :status] :success)
         (assoc-in [:editor :connections :list] connections)
         (cond-> updated-selected
           (assoc-in [:editor :connections :selected] updated-selected))
         (cond-> (seq updated-multi-selected)
           (assoc-in [:editor :multi-connections :selected] updated-multi-selected))))))

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
           [:dispatch [:database-schema->clear-schema]]
           [:dispatch [:primary-connection/update-runbooks]]]})))

(rf/reg-event-fx
 :primary-connection/clear-selected
 (fn [{:keys [db]} _]
   {:db (-> db
            (assoc-in [:editor :connections :selected] nil)
            (assoc-in [:editor :multi-connections :selected] []))
    :fx [[:dispatch [:primary-connection/persist-selected]]
         [:dispatch [:multiple-connections/persist]]
         [:dispatch [:database-schema->clear-schema]]
         [:dispatch [:primary-connection/update-runbooks]]]}))

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
         connection-name (:name parsed)
         connections (get-in db [:editor :connections :list])
         updated-connection (when (and connection-name connections)
                              (first (filter #(= (:name %) connection-name) connections)))]
     (if updated-connection
       {:db (assoc-in db [:editor :connections :selected] updated-connection)}
       {:db (assoc-in db [:editor :connections :selected] parsed)}))))

(rf/reg-event-fx
 :primary-connection/update-runbooks
 (fn [{:keys [db]} _]
   (let [primary-connection (get-in db [:editor :connections :selected])
         selected-connections (get-in db [:editor :multi-connections :selected] [])]
     {:fx [[:dispatch [:runbooks-plugin->get-runbooks
                       (map :name (concat
                                   (when primary-connection [primary-connection])
                                   selected-connections))]]]})))

;; Dialog Events (for compact UI)
(rf/reg-event-db
 :primary-connection/toggle-dialog
 (fn [db [_ open?]]
   (assoc-in db [:editor :connections :dialog-open?] open?)))

;; Subscriptions
(rf/reg-sub
 :primary-connection/status
 (fn [db]
   (get-in db [:editor :connections :status])))

(rf/reg-sub
 :primary-connection/list
 (fn [db]
   (get-in db [:editor :connections :list])))

(rf/reg-sub
 :primary-connection/error
 (fn [db]
   (get-in db [:editor :connections :error])))

(rf/reg-sub
 :primary-connection/selected
 (fn [db]
   (get-in db [:editor :connections :selected])))

(rf/reg-sub
 :primary-connection/filter
 (fn [db]
   (get-in db [:editor :connections :filter])))

(rf/reg-sub
 :primary-connection/filtered
 :<- [:primary-connection/list]
 :<- [:primary-connection/filter]
 (fn [[connections filter-text]]
   (if (empty? filter-text)
     connections
     (filter #(string/includes?
               (string/lower-case (:name %))
               (string/lower-case filter-text))
             connections))))

(rf/reg-sub
 :primary-connection/dialog-open?
 (fn [db]
   (get-in db [:editor :connections :dialog-open?])))
