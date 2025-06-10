(ns webapp.ai-data-masking.events
  (:require
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
    :fx [[:dispatch [:show-snackbar {:text (str "Error creating rule: " error) :level :error}]]]}))

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
    :fx [[:dispatch [:show-snackbar {:text (str "Error updating rule: " error) :level :error}]]]}))

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
    :fx [[:dispatch [:show-snackbar {:text (str "Error deleting rule: " error) :level :error}]]]}))

;; Clear active rule
(rf/reg-event-db
 :ai-data-masking->clear-active-rule
 (fn [db _]
   (assoc-in db [:ai-data-masking :active-rule] {:status :idle :data nil})))

;; Get connections for form
(rf/reg-event-fx
 :ai-data-masking->get-connections
 (fn [_ _]
   {:dispatch [:connections->get-connections]}))
