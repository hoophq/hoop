(ns webapp.webclient.events.multiple-connections
  (:require
   [cljs.reader :as reader]
   [clojure.string :as cs]
   [re-frame.core :as rf]))

;; Toggle seleção de uma conexão
(rf/reg-event-fx
 :multiple-connections/toggle
 (fn [{:keys [db]} [_ connection]]
   (let [primary-connection (get-in db [:editor :connections :selected])]

     ;; 🚫 REGRA 1: Sem primary = sem múltiplas
     (if-not primary-connection
       {:fx [[:dispatch [:dialog->open
                         {:title "Primary Connection Required"
                          :text "Please select a primary connection first before adding multiple connections."
                          :action-button? false}]]]}

       ;; 🚫 REGRA 2: Primary não pode ser múltipla
       (if (= (:name connection) (:name primary-connection))
         {:fx [[:dispatch [:dialog->open
                           {:title "Cannot Add Primary Connection"
                            :text "The primary connection cannot be added to multiple connections. It's already included by default."
                            :action-button? false}]]]}

         ;; ✅ Lógica normal de toggle (preservada)
         (let [current-selections (get-in db [:editor :multi-connections :selected] [])
               updated-selections (if (some #(= (:name %) (:name connection)) current-selections)
                                    (filterv #(not= (:name %) (:name connection)) current-selections)
                                    (conj current-selections connection))]
           {:db (assoc-in db [:editor :multi-connections :selected] updated-selections)
            :fx [[:dispatch [:multiple-connections/persist]]
                 [:dispatch [:primary-connection/update-runbooks]]]}))))))

;; Persiste seleções no localStorage
(rf/reg-event-fx
 :multiple-connections/persist
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
 :multiple-connections/load-persisted
 (fn [{:keys [db]} _]
   (let [primary-connection (get-in db [:editor :connections :selected])
         saved (.getItem js/localStorage "run-connection-list-selected")]

     ;; Só carrega se há primary para validar compatibilidade
     (if (and primary-connection saved)
       (let [parsed (reader/read-string saved)
             connections (get-in db [:editor :connections :list])
             valid-selections (when (and parsed connections)
                                (vec (keep (fn [saved-conn]
                                             (let [conn (first (filter #(= (:name %) (:name saved-conn)) connections))]
                                               ;; Só mantém se compatível com primary E não é a própria primary
                                               (when (and conn
                                                          (= (:type conn) (:type primary-connection))
                                                          (= (:subtype conn) (:subtype primary-connection))
                                                          (not= (:name conn) (:name primary-connection)))
                                                 conn)))
                                           parsed)))]
         {:db (assoc-in db [:editor :multi-connections :selected] (or valid-selections []))})

       ;; Sem primary = força limpeza
       {:db (assoc-in db [:editor :multi-connections :selected] [])
        :fx [[:dispatch [:multiple-connections/persist]]]}))))

;; Limpa todas as seleções
(rf/reg-event-fx
 :multiple-connections/clear
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:editor :multi-connections :selected] [])
    :fx [[:dispatch [:multiple-connections/persist]]
         [:dispatch [:primary-connection/update-runbooks]]]}))

;; Filtra conexões
(rf/reg-event-db
 :multiple-connections/filter
 (fn [db [_ filter-text]]
   (assoc-in db [:editor :multi-connections :filter] filter-text)))

;; -- Subscriptions --

(rf/reg-sub
 :multiple-connections/selected
 (fn [db]
   (get-in db [:editor :multi-connections :selected] [])))

(rf/reg-sub
 :multiple-connections/filter
 (fn [db]
   (get-in db [:editor :multi-connections :filter] "")))

(rf/reg-sub
 :multiple-connections/filtered-connections
 :<- [:primary-connection/list]
 :<- [:multiple-connections/filter]
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

;; ---- Composição: Selectors Centralizados ----

(rf/reg-sub
 :execution/target-connections
 :<- [:primary-connection/selected]
 :<- [:multiple-connections/selected]
 (fn [[primary multiples]]
   (if primary
     (cons primary multiples)        ; Primary sempre primeiro
     [])))                           ; Sem primary = sem execução

(rf/reg-sub
 :execution/total-count
 :<- [:primary-connection/selected]
 :<- [:multiple-connections/selected]
 (fn [[primary multiples]]
   (+ (if primary 1 0) (count multiples))))

(rf/reg-sub
 :execution/is-single-mode
 :<- [:multiple-connections/selected]
 (fn [multiples]
   (empty? multiples)))             ; Só primary = single mode

(rf/reg-sub
 :execution/can-execute
 :<- [:primary-connection/selected]
 :<- [:multiple-connections/selected]
 (fn [[primary multiples]]
   (and (some? primary)             ; Tem primary
        (every? #(= (:type %) (:type primary)) multiples)  ; Todas compatíveis
        (every? #(not= (:name %) (:name primary)) multiples))))
