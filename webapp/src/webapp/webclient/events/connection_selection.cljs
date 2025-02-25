(ns webapp.webclient.events.connection-selection
  (:require
   [cljs.reader :as reader]
   [clojure.string :as cs]
   [re-frame.core :as rf]))

;; Toggle seleção de uma conexão
(rf/reg-event-fx
 :connection-selection/toggle
 (fn [{:keys [db]} [_ connection]]
   (let [current-selections (get-in db [:editor :multi-connections :selected] [])
         updated-selections (if (some #(= (:name %) (:name connection)) current-selections)
                              (filterv #(not= (:name %) (:name connection)) current-selections)
                              (conj current-selections connection))]
     {:db (assoc-in db [:editor :multi-connections :selected] updated-selections)
      :fx [[:dispatch [:connection-selection/persist]]]})))

;; Persiste seleções no localStorage
(rf/reg-event-fx
 :connection-selection/persist
 (fn [{:keys [db]} _]
   (let [selections (get-in db [:editor :multi-connections :selected])]
     (.setItem js/localStorage
               "run-connection-list-selected"
               (pr-str selections))
     {})))

;; Carrega seleções do localStorage
(rf/reg-event-fx
 :connection-selection/load-persisted
 (fn [{:keys [db]} _]
   (let [saved (.getItem js/localStorage "run-connection-list-selected")
         parsed (when saved (reader/read-string saved))]
     {:db (assoc-in db [:editor :multi-connections :selected] (or parsed []))})))

;; Limpa todas as seleções
(rf/reg-event-db
 :connection-selection/clear
 (fn [db _]
   (assoc-in db [:editor :multi-connections :selected] [])))

;; Filtra conexões
(rf/reg-event-db
 :connection-selection/filter
 (fn [db [_ filter-text]]
   (assoc-in db [:editor :multi-connections :filter] filter-text)))

;; -- Subscriptions --

(rf/reg-sub
 :connection-selection/selected
 (fn [db]
   (get-in db [:editor :multi-connections :selected] [])))

(rf/reg-sub
 :connection-selection/filter
 (fn [db]
   (get-in db [:editor :multi-connections :filter] "")))

(rf/reg-sub
 :connection-selection/filtered-connections
 :<- [:connections/list]
 :<- [:connection-selection/filter]
 (fn [[connections filter-text]]
   (if (empty? filter-text)
     connections
     (filter #(or
               (cs/includes?
                (cs/lower-case (:name %))
                (cs/lower-case filter-text))
               (cs/includes?
                (cs/lower-case (:type %))
                (cs/lower-case filter-text)))
             connections))))
