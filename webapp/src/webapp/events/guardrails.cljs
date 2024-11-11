(ns webapp.events.guardrails
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :guardrails->get-all
 (fn [{:keys [db]} [_ _]]
   {:fx [[:dispatch
          [:fetch {:method "GET"
                   :uri "/guardrails"
                   :on-success (fn [guardrails]
                                 (rf/dispatch [:guardrails->set-all guardrails]))
                   :on-failure (fn [error]
                                 (rf/dispatch [:guardrails->set-all nil error]))}]]]
    :db (assoc db :guardrails->active-guardrail {:status :loading
                                                 :data []})}))

(rf/reg-event-fx
 :guardrails->get-by-id
 (fn [{:keys [db]} [_ id]]
   {:fx [[:dispatch
          [:fetch {:method "GET"
                   :uri (str "/guardrails/" id)
                   :on-success (fn [guardrails]
                                 (rf/dispatch [:guardrails->set-active-guardrail guardrails]))
                   :on-failure (fn [error]
                                 (rf/dispatch [:guardrails->set-all nil error]))}]]]
    :db (assoc db :guardrails->all {:status :loading
                                    :data {}})}))

(rf/reg-event-db
 :guardrails->set-all
 (fn [db [_ guardrails]]
   (assoc db :guardrails->list {:status :ready :data guardrails})))

;; Sanitize guardrail rules before sending to the server
(defn convert-preset-rules [rules]
  (mapv (fn [{:keys [type words pattern]}]
          {:type type
           :words words
           :pattern pattern})
        rules))

(defn remove-empty-rules [rules]
  (remove (fn [rule]
            (or (and (empty? (:words rule))
                     (empty? (:pattern rule)))
                (empty? (:type rule))))
          rules))

(defn  process-rules [rules]
  {:rules (-> rules
              convert-preset-rules
              remove-empty-rules)})

(rf/reg-event-fx
 :guardrails->create
 (fn [_ [_ guardrails]]
   (let [sanitize-guardrail (fn [guardrail]
                              (-> guardrail
                                  (update :input process-rules)
                                  (update :output process-rules)))]
     {:fx [[:dispatch
            [:fetch {:method "POST"
                     :uri "/guardrails"
                     :body (sanitize-guardrail guardrails)
                     :on-success (fn []
                                   (rf/dispatch [:guardrails->get-all])
                                   (rf/dispatch [:navigate :guardrails]))
                     :on-failure (fn [error]
                                   (println :guardrails->create guardrails error))}]]]})))

(rf/reg-event-fx
 :guardrails->update-by-id
 (fn [_ [_ guardrails]]
   (let [sanitize-guardrail (fn [guardrail]
                              (-> guardrail
                                  (update :input process-rules)
                                  (update :output process-rules)))]
     {:fx [[:dispatch
            [:fetch {:method "PUT"
                     :uri (str "/guardrails/" (:id guardrails))
                     :body (sanitize-guardrail guardrails)
                     :on-success (fn []
                                   (rf/dispatch [:guardrails->get-all])
                                   (rf/dispatch [:navigate :guardrails]))
                     :on-failure (fn [error]
                                   (println :guardrails->update-by-id guardrails error))}]]]})))

(rf/reg-event-fx
 :guardrails->delete-by-id
 (fn [_ [_ id]]
   {:fx [[:dispatch
          [:fetch {:method "DELETE"
                   :uri (str "/guardrails/" id)
                   :on-success (fn []
                                 (rf/dispatch [:guardrails->get-all])
                                 (rf/dispatch [:navigate :guardrails]))
                   :on-failure (fn [error]
                                 (println :guardrails->delete-by-id id error))}]]]}))

;; This event manages the state for when editing
;; or creating a new guardrails rule
(rf/reg-event-fx
 :guardrails->set-active-guardrail
 (fn
   [{:keys [db]} [_ rule]]
   (let [{:keys [id name description
                 input output]} rule
         rule-builder (fn [rule]
                        {:_id (or (:_id rule) random-uuid) ;; internal use
                         :type (:type rule) ; :deny-words or :pattern
                         :words (:words rule)
                         :pattern (:pattern rule)})
         rule-schema (merge
                      {:id (or id "")
                       :name (or name "")
                       :description (or description "")}
                      (if (seq (:rules input))
                        {:input (mapv rule-builder (:rules input))}
                        {:input [{:type "" :rule "" :details ""}]})
                      (if (seq (:rules output))
                        {:output (mapv rule-builder (:rules output))}
                        {:output [{:type "" :rule "" :details ""}]}))]
     {:db (assoc db :guardrails->active-guardrail {:status :ready
                                                   :data rule-schema})})))

;; SUBSCRIPTIONS
(rf/reg-sub
 :guardrails->list
 (fn [db _]
   (get-in db [:guardrails->list :data])))

(rf/reg-sub
 :guardrails->active-guardrail
 (fn [db _]
   (get-in db [:guardrails->active-guardrail])))


