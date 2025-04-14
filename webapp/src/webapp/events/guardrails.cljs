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
    :db (assoc db :guardrails->list {:status :loading
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
    :db (assoc db :guardrails->active-guardrail {:status :loading
                                                 :data {}})}))

(rf/reg-event-db
 :guardrails->set-all
 (fn [db [_ guardrails]]
   (assoc db :guardrails->list {:status :ready :data guardrails})))

;; Sanitize guardrail rules before sending to the server
(defn convert-preset-rules [rules]
  (mapv (fn [{:keys [type words pattern_regex]}]
          {:type type
           :words words
           :pattern_regex pattern_regex})
        rules))

(defn remove-empty-rules [rules]
  (remove (fn [rule]
            (or (and (empty? (:words rule))
                     (empty? (:pattern_regex rule)))
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
                 connection_ids
                 input output]} rule
         rule-builder (fn [rule]
                        {:_id (or (:_id rule) (random-uuid)) ;; internal use
                         :type (:type rule) ; :deny_words_list or :pattern
                         :words (:words rule)
                         :pattern_regex (:pattern_regex rule)})
         rule-schema (merge
                      {:id (or id "")
                       :name (or name "")
                       :description (or description "")
                       :connection_ids (or connection_ids [])}
                      (if (seq (:rules input))
                        {:input (mapv rule-builder (:rules input))}
                        {:input [{:type "" :rule "" :details ""}]})
                      (if (seq (:rules output))
                        {:output (mapv rule-builder (:rules output))}
                        {:output [{:type "" :rule "" :details ""}]}))]
     {:db (assoc db :guardrails->active-guardrail {:status :ready
                                                   :data rule-schema})})))

(rf/reg-event-fx
 :guardrails->clear-active-guardrail
 (fn
   [{:keys [db]} [_]]
   {:db (assoc db :guardrails->active-guardrail {:status :loading
                                                 :data {}})
    :fx [[:dispatch
          [:guardrails->set-active-guardrail {:id ""
                                              :name ""
                                              :description ""
                                              :connection_ids []
                                              :input [{:type "" :rule "" :details ""}]
                                              :output [{:type "" :rule "" :details ""}]}]]]}))

;; Get connections for guardrails
(rf/reg-event-fx
 :guardrails->get-connections
 (fn [{:keys [db]} _]
   {:db (assoc db :guardrails->connections-list {:status :loading :data []})
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/connections"
                             :on-success #(rf/dispatch [:guardrails->set-connections %])
                             :on-failure #(rf/dispatch [:guardrails->set-connections-error %])}]]]}))

(rf/reg-event-db
 :guardrails->set-connections
 (fn [db [_ connections]]
   (assoc db :guardrails->connections-list {:status :ready :data connections})))

(rf/reg-event-db
 :guardrails->set-connections-error
 (fn [db [_ error]]
   (assoc db :guardrails->connections-list {:status :error :error error :data []})))

;; SUBSCRIPTIONS
(rf/reg-sub
 :guardrails->list
 (fn [db _]
   (get-in db [:guardrails->list])))

(rf/reg-sub
 :guardrails->active-guardrail
 (fn [db _]
   (get-in db [:guardrails->active-guardrail])))

(rf/reg-sub
 :guardrails->connections-list
 (fn [db _]
   (get-in db [:guardrails->connections-list])))
