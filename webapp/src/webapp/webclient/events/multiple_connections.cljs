(ns webapp.webclient.events.multiple-connections
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
      :fx [[:dispatch [:connection-selection/persist]]
           [:dispatch [:connections/update-runbooks]]]})))

;; Persiste seleções no localStorage
(rf/reg-event-fx
 :connection-selection/persist
 (fn [{:keys [db]} _]
   (let [selections (get-in db [:editor :multi-connections :selected])
         ;; Salva apenas os nomes das conexões
         names-only (mapv #(hash-map :name (:name %)) selections)]
     (.setItem js/localStorage
               "run-connection-list-selected"
               (pr-str names-only))
     {})))

;; Carrega seleções do localStorage
(rf/reg-event-fx
 :connection-selection/load-persisted
 (fn [{:keys [db]} _]
   (let [saved (.getItem js/localStorage "run-connection-list-selected")
         parsed (when saved (reader/read-string saved))
         ;; Buscar conexões atualizadas da lista
         connections (get-in db [:editor :connections :list])
         updated-selections (when (and parsed connections)
                              (vec (keep (fn [saved-conn]
                                           (first (filter #(= (:name %) (:name saved-conn))
                                                          connections)))
                                         parsed)))]
     {:db (assoc-in db [:editor :multi-connections :selected] (or updated-selections []))})))

;; Limpa todas as seleções
(rf/reg-event-fx
 :connection-selection/clear
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:editor :multi-connections :selected] [])
    :fx [[:dispatch [:connection-selection/persist]]
         [:dispatch [:connections/update-runbooks]]]}))

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
