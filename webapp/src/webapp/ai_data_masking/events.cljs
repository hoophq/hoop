(ns webapp.ai-data-masking.events
  (:require
   [clojure.string :as cs]
   [re-frame.core :as rf]))

;; Get all AI Data Masking rules
(rf/reg-event-fx
 :ai-data-masking->get-all
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:ai-data-masking :list :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/datamasking-rules"
                             :on-success #(rf/dispatch [:ai-data-masking->get-all-success %])
                             :on-failure #(rf/dispatch [:ai-data-masking->get-all-failure %])}]]]}))

(rf/reg-event-db
 :ai-data-masking->get-all-success
 (fn [db [_ data]]
   (-> db
       (assoc-in [:ai-data-masking :list :status] :success)
       (assoc-in [:ai-data-masking :list :data] data))))

(rf/reg-event-db
 :ai-data-masking->get-all-failure
 (fn [db [_ error]]
   (-> db
       (assoc-in [:ai-data-masking :list :status] :error)
       (assoc-in [:ai-data-masking :list :error] error))))

;; Get specific AI Data Masking rule by ID
(rf/reg-event-fx
 :ai-data-masking->get-by-id
 (fn [{:keys [db]} [_ rule-id]]
   {:db (assoc-in db [:ai-data-masking :active-rule :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/datamasking-rules/" rule-id)
                             :on-success #(rf/dispatch [:ai-data-masking->get-by-id-success %])
                             :on-failure #(rf/dispatch [:ai-data-masking->get-by-id-failure %])}]]]}))

(rf/reg-event-db
 :ai-data-masking->get-by-id-success
 (fn [db [_ data]]
   (-> db
       ;; Clear any existing connections state to prevent stale data
       (update-in [:ai-data-masking :active-rule :data] dissoc :connections :connections-loading :connections-error)
       (assoc-in [:ai-data-masking :active-rule :status] :success)
       (assoc-in [:ai-data-masking :active-rule :data] data))))

(rf/reg-event-db
 :ai-data-masking->get-by-id-failure
 (fn [db [_ error]]
   (-> db
       (assoc-in [:ai-data-masking :active-rule :status] :error)
       (assoc-in [:ai-data-masking :active-rule :error] error))))

;; Create AI Data Masking rule
(rf/reg-event-fx
 :ai-data-masking->create
 (fn [{:keys [db]} [_ data]]
   {:db (assoc-in db [:ai-data-masking :submitting?] true)
    :fx [[:dispatch [:fetch {:method "POST"
                             :uri "/datamasking-rules"
                             :body data
                             :on-success #(rf/dispatch [:ai-data-masking->create-success %])
                             :on-failure #(rf/dispatch [:ai-data-masking->create-failure %])}]]]}))

(rf/reg-event-fx
 :ai-data-masking->create-success
 (fn [{:keys [db]} [_ response]]
   {:db (assoc-in db [:ai-data-masking :submitting?] false)
    :fx [[:dispatch [:navigate :ai-data-masking]]
         [:dispatch [:ai-data-masking->get-all]]]}))

(rf/reg-event-fx
 :ai-data-masking->create-failure
 (fn [{:keys [db]} [_ error]]
   {:db (-> db
            (assoc-in [:ai-data-masking :submitting?] false)
            (assoc-in [:ai-data-masking :error] error))
    :fx [[:dispatch [:show-snackbar {:text "Failed to create data masking rule"
                                     :level :error
                                     :details error}]]]}))

;; Update AI Data Masking rule
(rf/reg-event-fx
 :ai-data-masking->update-by-id
 (fn [{:keys [db]} [_ data]]
   (let [rule-id (get-in db [:ai-data-masking :active-rule :data :id])]
     {:db (assoc-in db [:ai-data-masking :submitting?] true)
      :fx [[:dispatch [:fetch {:method "PUT"
                               :uri (str "/datamasking-rules/" rule-id)
                               :body data
                               :on-success #(rf/dispatch [:ai-data-masking->update-success %])
                               :on-failure #(rf/dispatch [:ai-data-masking->update-failure %])}]]]})))

(rf/reg-event-fx
 :ai-data-masking->update-success
 (fn [{:keys [db]} [_ response]]
   {:db (assoc-in db [:ai-data-masking :submitting?] false)
    :fx [[:dispatch [:navigate :ai-data-masking]]
         [:dispatch [:ai-data-masking->get-all]]]}))

(rf/reg-event-fx
 :ai-data-masking->update-failure
 (fn [{:keys [db]} [_ error]]
   {:db (-> db
            (assoc-in [:ai-data-masking :submitting?] false)
            (assoc-in [:ai-data-masking :error] error))
    :fx [[:dispatch [:show-snackbar {:text "Failed to update data masking rule"
                                     :level :error
                                     :details error}]]]}))

;; Delete AI Data Masking rule
(rf/reg-event-fx
 :ai-data-masking->delete-by-id
 (fn [{:keys [db]} [_ rule-id]]
   {:db (assoc-in db [:ai-data-masking :submitting?] true)
    :fx [[:dispatch [:fetch {:method "DELETE"
                             :uri (str "/datamasking-rules/" rule-id)
                             :on-success #(rf/dispatch [:ai-data-masking->delete-success])
                             :on-failure #(rf/dispatch [:ai-data-masking->delete-failure %])}]]]}))

(rf/reg-event-fx
 :ai-data-masking->delete-success
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:ai-data-masking :submitting?] false)
    :fx [[:dispatch [:navigate :ai-data-masking]]
         [:dispatch [:ai-data-masking->get-all]]
         [:dispatch [:show-snackbar {:text "Rule deleted successfully" :level :success}]]]}))

(rf/reg-event-fx
 :ai-data-masking->delete-failure
 (fn [{:keys [db]} [_ error]]
   {:db (-> db
            (assoc-in [:ai-data-masking :submitting?] false)
            (assoc-in [:ai-data-masking :error] error))
    :fx [[:dispatch [:show-snackbar {:text "Failed to delete data masking rule"
                                     :level :error
                                     :details error}]]]}))

;; Clear active rule
(rf/reg-event-db
 :ai-data-masking->clear-active-rule
 (fn [db _]
   (-> db
       ;; Clear any existing connections state to prevent stale data
       (update-in [:ai-data-masking :active-rule :data] dissoc :connections :connections-loading :connections-error)
       (assoc-in [:ai-data-masking :active-rule] {:status :idle :data nil}))))

(rf/reg-event-fx
 :ai-data-masking/get-selected-connections
 (fn [{:keys [db]} [_ connection-ids]]
   (if (seq connection-ids)
     (let [page-size 30
           base-uri "/connections"
           chunks (partition-all page-size connection-ids)
           num-batches (count chunks)
           mk-uri (fn [ids]
                    (let [query-params [(str "connection_ids=" (cs/join "," ids))
                                        "page=1"
                                        (str "page_size=" page-size)]]
                      (str base-uri "?" (cs/join "&" query-params))))
           fx-requests (mapv (fn [ids]
                               [:dispatch
                                [:fetch {:method "GET"
                                         :uri (mk-uri ids)
                                         :on-success (fn [response]
                                                       (rf/dispatch [:ai-data-masking/accumulate-selected-connections (:data response)]))
                                         :on-failure (fn [error]
                                                       (rf/dispatch [:ai-data-masking/accumulate-selected-connections-error error]))}]])
                             chunks)]
       {:db (-> db
                (assoc-in [:ai-data-masking :active-rule :data :connections-loading]
                          {:remaining num-batches
                           :acc []
                           :errors []}))
        :fx fx-requests})
     {:db (-> db
              (assoc-in [:ai-data-masking :active-rule :data :connections] [])
              (assoc-in [:ai-data-masking :active-rule :data :connections-loading]
                        {:remaining 0 :acc [] :errors []}))})))

(rf/reg-event-fx
 :ai-data-masking/accumulate-selected-connections
 (fn [{:keys [db]} [_ connections]]
   (let [{:keys [remaining acc]} (get-in db [:ai-data-masking :active-rule :data :connections-loading] {:remaining 0 :acc []})
         new-remaining (dec remaining)
         new-acc (into acc connections)]
     (if (pos? new-remaining)
       {:db (assoc-in db [:ai-data-masking :active-rule :data :connections-loading]
                      {:remaining new-remaining
                       :acc new-acc
                       :errors (:errors (get-in db [:ai-data-masking :active-rule :data :connections-loading]))})}
       {:db (update-in db [:ai-data-masking :active-rule :data] dissoc :connections-loading)
        :fx [[:dispatch [:ai-data-masking/set-selected-connections new-acc]]]}))))

(rf/reg-event-fx
 :ai-data-masking/accumulate-selected-connections-error
 (fn [{:keys [db]} [_ error]]
   (let [{:keys [remaining acc errors]} (get-in db [:ai-data-masking :active-rule :data :connections-loading] {:remaining 0 :acc [] :errors []})
         new-remaining (dec remaining)
         new-errors (conj (vec errors) error)]
     (if (pos? new-remaining)
       {:db (assoc-in db [:ai-data-masking :active-rule :data :connections-loading]
                      {:remaining new-remaining
                       :acc acc
                       :errors new-errors})}
       {:db (update-in db [:ai-data-masking :active-rule :data] dissoc :connections-loading)
        :fx [[:dispatch [:ai-data-masking/set-selected-connections-error new-errors]]]}))))

(rf/reg-event-db
 :ai-data-masking/set-selected-connections
 (fn [db [_ connections]]
   (assoc-in db [:ai-data-masking :active-rule :data :connections] connections)))

(rf/reg-event-db
 :ai-data-masking/set-selected-connections-error
 (fn [db [_ error]]
   (-> db
       (assoc-in [:ai-data-masking :active-rule :data :connections] [])
       (assoc-in [:ai-data-masking :active-rule :data :connections-error] error))))