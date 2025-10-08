(ns webapp.webclient.events.multiple-connections
  (:require
   [cljs.reader :as reader]
   [re-frame.core :as rf]))

;; Toggle selection of a connection
(rf/reg-event-fx
 :multiple-connections/toggle
 (fn [{:keys [db]} [_ connection]]
   (let [primary-connection (get-in db [:editor :connections :selected])]

     ;; 🚫 RULE 1: No primary = no multiples
     (if-not primary-connection
       {:fx [[:dispatch [:dialog->open
                         {:title "Primary Connection Required"
                          :text "Please select a primary connection first before adding multiple connections."
                          :action-button? false}]]]}

       ;; 🚫 RULE 2: Primary cannot be multiple
       (if (= (:name connection) (:name primary-connection))
         {:fx [[:dispatch [:dialog->open
                           {:title "Cannot Add Primary Connection"
                            :text "The primary connection cannot be added to multiple connections. It's already included by default."
                            :action-button? false}]]]}

         ;; ✅ Normal toggle logic (preserved)
         (let [current-selections (get-in db [:editor :multi-connections :selected] [])
               updated-selections (if (some #(= (:name %) (:name connection)) current-selections)
                                    (filterv #(not= (:name %) (:name connection)) current-selections)
                                    (conj current-selections connection))]
           {:db (assoc-in db [:editor :multi-connections :selected] updated-selections)
            :fx [[:dispatch [:multiple-connections/persist]]]}))))))

;; Persist selections to localStorage
(rf/reg-event-fx
 :multiple-connections/persist
 (fn [{:keys [db]} _]
   (let [selections (get-in db [:editor :multi-connections :selected])
         ;; Save only connection names
         names-only (mapv #(hash-map :name (:name %)) selections)]
     (.setItem js/localStorage
               "run-connection-list-selected"
               (pr-str names-only))
     {})))

;; Load selections from localStorage
(rf/reg-event-fx
 :multiple-connections/load-persisted
 (fn [{:keys [db]} _]
   (let [primary-connection (get-in db [:editor :connections :selected])
         saved (.getItem js/localStorage "run-connection-list-selected")]

     ;; Only load if there's a primary to validate compatibility
     (if (and primary-connection saved)
       (let [parsed (reader/read-string saved)
             connections (get-in db [:editor :connections :list])
             valid-selections (when (and parsed connections)
                                (vec (keep (fn [saved-conn]
                                             (let [conn (first (filter #(= (:name %) (:name saved-conn)) connections))]
                                               ;; Only keep if compatible with primary AND not the primary itself
                                               (when (and conn
                                                          (= (:type conn) (:type primary-connection))
                                                          (= (:subtype conn) (:subtype primary-connection))
                                                          (not= (:name conn) (:name primary-connection)))
                                                 conn)))
                                           parsed)))]
         {:db (assoc-in db [:editor :multi-connections :selected] (or valid-selections []))})

       ;; No primary = force cleanup
       {:db (assoc-in db [:editor :multi-connections :selected] [])
        :fx [[:dispatch [:multiple-connections/persist]]]}))))

;; Clear all selections
(rf/reg-event-fx
 :multiple-connections/clear
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:editor :multi-connections :selected] [])
    :fx [[:dispatch [:multiple-connections/persist]]]}))

;; -- Subscriptions --

(rf/reg-sub
 :multiple-connections/selected
 (fn [db]
   (get-in db [:editor :multi-connections :selected] [])))

;; ---- Composition: Centralized Selectors ----

(rf/reg-sub
 :execution/target-connections
 :<- [:primary-connection/selected]
 :<- [:multiple-connections/selected]
 (fn [[primary multiples]]
   (if primary
     (cons primary multiples)        ; Primary always first
     [])))                           ; No primary = no execution

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
   (empty? multiples)))             ; Only primary = single mode

(rf/reg-sub
 :execution/can-execute
 :<- [:primary-connection/selected]
 :<- [:multiple-connections/selected]
 (fn [[primary multiples]]
   (and (some? primary)             ; Has primary
        (every? #(= (:type %) (:type primary)) multiples)  ; All compatible
        (every? #(not= (:name %) (:name primary)) multiples))))  ; None duplicates primary
