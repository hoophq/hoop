(ns webapp.webclient.events.connections
  (:require
   [clojure.edn :refer [read-string]]
   [clojure.string :as string]
   [re-frame.core :as rf]))

;; Efeito para buscar conexões
(rf/reg-fx
 :fetch-connections
 (fn [_]
   (rf/dispatch [:fetch
                 {:method "GET"
                  :uri "/connections"
                  :on-success #(rf/dispatch [:connections/set-list %])
                  :on-failure #(rf/dispatch [:connections/set-error %])}])))

;; Events
(rf/reg-event-fx
 :connections/initialize
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:editor :connections :status] :loading)
    :fx [[:fetch-connections]
         [:dispatch-later {:ms 2000 :dispatch [:connections/update-runbooks]}]]}))

(rf/reg-event-db
 :connections/set-error
 (fn [db [_ error]]
   (-> db
       (assoc-in [:editor :connections :status] :error)
       (assoc-in [:editor :connections :error] error))))

(rf/reg-event-db
 :connections/set-list
 (fn [db [_ connections]]
   (-> db
       (assoc-in [:editor :connections :status] :success)
       (assoc-in [:editor :connections :list] connections))))

(rf/reg-event-db
 :connections/set-filter
 (fn [db [_ filter-text]]
   (assoc-in db [:editor :connections :filter] filter-text)))

(rf/reg-event-fx
 :connections/set-selected
 (fn [{:keys [db]} [_ connection]]
   {:db (assoc-in db [:editor :connections :selected] connection)
    :fx [[:dispatch [:editor-plugin/clear-language]]
         [:dispatch [:connections/persist-selected]]
         [:dispatch [:database-schema->clear-schema]]
         [:dispatch [:connections/update-runbooks]]]}))

(rf/reg-event-fx
 :connections/clear-selected
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:editor :connections :selected] nil)
    :fx [[:dispatch [:connections/persist-selected]]
         [:dispatch [:database-schema->clear-schema]]
         [:dispatch [:connections/update-runbooks]]]}))

(rf/reg-event-fx
 :connections/persist-selected
 (fn [{:keys [db]} _]
   (let [selected (get-in db [:editor :connections :selected])]
     (.setItem js/localStorage
               "selected-connection"
               (when selected (pr-str selected)))
     {})))

(rf/reg-event-fx
 :connections/load-persisted
 (fn [{:keys [db]} _]
   (let [saved (.getItem js/localStorage "selected-connection")
         parsed (when (and saved (not= saved "null"))
                  (read-string saved))]
     {:db (assoc-in db [:editor :connections :selected] parsed)})))

(rf/reg-event-fx
 :connections/update-runbooks
 (fn [{:keys [db]} _]
   (let [primary-connection (get-in db [:editor :connections :selected])
         selected-connections (get-in db [:editor :multi-connections :selected] [])]
     {:fx [[:dispatch [:runbooks-plugin->get-runbooks
                       (map :name (concat
                                   (when primary-connection [primary-connection])
                                   selected-connections))]]]})))

;; Subscriptions
(rf/reg-sub
 :connections/status
 (fn [db]
   (get-in db [:editor :connections :status])))

(rf/reg-sub
 :connections/list
 (fn [db]
   (get-in db [:editor :connections :list])))

(rf/reg-sub
 :connections/error
 (fn [db]
   (get-in db [:editor :connections :error])))

(rf/reg-sub
 :connections/selected
 (fn [db]
   (get-in db [:editor :connections :selected])))

(rf/reg-sub
 :connections/filter
 (fn [db]
   (get-in db [:editor :connections :filter])))

(rf/reg-sub
 :connections/filtered
 :<- [:connections/list]
 :<- [:connections/filter]
 (fn [[connections filter-text]]
   (if (empty? filter-text)
     connections
     (filter #(string/includes?
               (string/lower-case (:name %))
               (string/lower-case filter-text))
             connections))))
