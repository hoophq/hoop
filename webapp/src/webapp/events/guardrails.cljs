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
     :db (assoc db :guardrails->all {:loading true
                                     :data (get-in db [:guardrails->list :data])})}))

(rf/reg-event-db
  :guardrails->set-all
  (fn
    [db [_ guardrails error]]
    (println :guardrails->set-all guardrails error)
    (assoc db :guardrails->list {:loading false :data guardrails})))

;; This event manages the state for when editing
;; or creating a new guardrails rule
(rf/reg-event-fx
  :guardrails->set-active-guardrail
  (fn
    [{:keys [db]} [_ {:keys [action rule-name description
                             input-rules output-rules]}]]
    (let [rule-builder (fn [rule]
                         {:_id (or (:_id rule) random-uuid) ;; internal use
                          :type (:type rule) ; :deny-words or :pattern
                          :words (:words rule)
                          :pattern (:pattern rule)})
          rule-schema (merge
                        {:action action ;; :create or :edit
                         :name (or rule-name "")
                         :description (or description "")}
                        (when input-rules
                          {:input (mapv rule-builder input-rules)})
                        (when output-rules
                          {:output (mapv rule-builder output-rules)}))]
    {:db (assoc db :guardrails->active-guardrail rule-schema)})))

;; this pushes a rule to the active guardrail
;; rule-type must be :input or :output
;; make sure `rule` is a vector [] and not a list '()
(rf/reg-event-db
  :guardrails->push-rule
  (fn
    [db [_ rule-type rule]]
    (let [current-rules (get-in db [:guardrails->active-guardrail rule-type])]
      (assoc-in db [:guardrails->active-guardrail rule-type]
                 (conj current-rules rule)))))

;; removes rules from list by its internal use :_id
;; rule-type must be :input or :output
;; ids is a vector of :_id's to remove
(rf/reg-event-db
  :guardrails->remove-rules
  (fn [db, [_ rule-type ids]]
    (let [current-rules (get-in db [:guardrails->active-guardrail rule-type])]
      (assoc-in db [:guardrails->active-guardrail rule-type]
                 (filterv #(not (contains? ids (:_id %)))
                          current-rules)))))

(rf/reg-sub
  :guardrails->list
  (fn [db _]
    (get-in db [:guardrails->list :data])))

(rf/reg-sub
  :guardrails->active-rule
  (fn [db _]
    (get-in db [:guardrails->active-guardrail])))


