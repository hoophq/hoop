(ns webapp.ai-data-masking.events
  (:require
   [re-frame.core :as rf]))

;; Mock data for development
(def mock-ai-data-masking-rules
  #_[]
  [{:id "1"
    :name "database-default_all"
    :description "Default rules for all Database connections"
    :connection_ids ["6bc464f1-6ae7-4540-99e9-1b89ea4c18bc" "5ef66188-4fe1-4736-88f6-3f7eb137344c"]
    :data_protection_method "content-full"
    :supported_entity_types [{:name "KEYS_AND_PASSWORDS" :values ["AUTH_TOKEN" "PASSWORD"]}
                             {:name "CUSTOM-SELECTION" :values ["EMAIL_ADDRESS"]}]
    :custom_entity_types [{:name "ZIP_CODE" :regex "\\b[0-9]{5}\\b" :score 0.01}
                          {:name "LICENSE_NUMBER" :regex "\\b[A-Z]{2}[0-9]{3}\\b" :score 0.05}]}])

;; Initialize AI Data Masking state
(rf/reg-event-db
 :ai-data-masking->initialize
 (fn [db _]
   (assoc db :ai-data-masking {:list {:status :idle :data []}
                               :active-rule {:status :idle :data nil}
                               :submitting? false})))

;; Get all AI Data Masking rules
(rf/reg-event-db
 :ai-data-masking->get-all
 (fn [db _]
   (assoc-in db [:ai-data-masking :list :status] :loading)))

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

;; Mock the get-all request for now
(rf/reg-event-fx
 :ai-data-masking->get-all
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:ai-data-masking :list :status] :loading)
    :dispatch-later [{:ms 1000 :dispatch [:ai-data-masking->get-all-success mock-ai-data-masking-rules]}]}))

;; Get specific AI Data Masking rule by ID
(rf/reg-event-fx
 :ai-data-masking->get-by-id
 (fn [{:keys [db]} [_ rule-id]]
   (let [rule (first (filter #(= (:id %) rule-id) mock-ai-data-masking-rules))]
     {:db (assoc-in db [:ai-data-masking :active-rule :status] :loading)
      :dispatch-later [{:ms 500 :dispatch [:ai-data-masking->get-by-id-success rule]}]})))

(rf/reg-event-db
 :ai-data-masking->get-by-id-success
 (fn [db [_ data]]
   (-> db
       (assoc-in [:ai-data-masking :active-rule :status] :success)
       (assoc-in [:ai-data-masking :active-rule :data] data))))

;; Create AI Data Masking rule
(rf/reg-event-fx
 :ai-data-masking->create
 (fn [{:keys [db]} [_ data]]
   (println "Creating AI Data Masking rule:" data)
   {:db (assoc-in db [:ai-data-masking :submitting?] true)
    :dispatch-later [{:ms 1500 :dispatch [:ai-data-masking->create-success]}]}))

(rf/reg-event-fx
 :ai-data-masking->create-success
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:ai-data-masking :submitting?] false)
    :fx [[:dispatch [:navigate :ai-data-masking]]
         [:dispatch [:ai-data-masking->get-all]]]}))

;; Update AI Data Masking rule
(rf/reg-event-fx
 :ai-data-masking->update-by-id
 (fn [{:keys [db]} [_ data]]
   (println "Updating AI Data Masking rule:" data)
   {:db (assoc-in db [:ai-data-masking :submitting?] true)
    :dispatch-later [{:ms 1500 :dispatch [:ai-data-masking->update-success]}]}))

(rf/reg-event-fx
 :ai-data-masking->update-success
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:ai-data-masking :submitting?] false)
    :fx [[:dispatch [:navigate :ai-data-masking]]
         [:dispatch [:ai-data-masking->get-all]]]}))

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
