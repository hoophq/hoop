(ns webapp.events.guardrails
  (:require
   [re-frame.core :as rf]
   [clojure.string :as cs]))

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
  (mapv (fn [{:keys [type words pattern_regex message]}]
          {:type type
           :words words
           :pattern_regex pattern_regex
           :message (or message "")})
        rules))

(defn- empty-rule-config?
  "True when a rule has no effective configuration and will not be persisted."
  [rule]
  (or (and (empty? (:words rule))
           (empty? (:pattern_regex rule)))
      (empty? (:type rule))))

(defn remove-empty-rules [rules]
  (remove empty-rule-config? rules))

(defn rules-with-orphan-message
  "Rules that would be dropped by remove-empty-rules while still carrying an
  admin-written custom error message. Saving would silently discard that
  message, so callers must surface an error instead."
  [rules]
  (filter #(and (empty-rule-config? %)
                (not (cs/blank? (:message %))))
          rules))

(defn- orphan-message-error
  "Returns an effects map with a visible error when any input/output rule has
  a custom error message but no configuration, nil otherwise."
  [guardrails]
  (let [orphans (concat (rules-with-orphan-message (:input guardrails))
                        (rules-with-orphan-message (:output guardrails)))
        n (count orphans)]
    (when (pos? n)
      {:fx [[:dispatch
             [:show-snackbar
              {:level :error
               :text (if (= n 1)
                       "A rule has a custom error message but no configured words or pattern. Configure the rule or clear its message before saving."
                       (str n " rules have a custom error message but no configured words or pattern. Configure the rules or clear their messages before saving."))}]]]})))

(defn  process-rules [rules]
  {:rules (-> rules
              convert-preset-rules
              remove-empty-rules)})

(rf/reg-event-fx
 :guardrails->create
 (fn [_ [_ guardrails]]
   (or
    (orphan-message-error guardrails)
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
                                    (let [error-message (or (:message error) (str error))]
                                      (rf/dispatch [:show-snackbar {:level :error
                                                                    :text error-message}])))}]]]}))))

(rf/reg-event-fx
 :guardrails->update-by-id
 (fn [_ [_ guardrails]]
   (or
    (orphan-message-error guardrails)
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
                                    (println :guardrails->update-by-id guardrails error))}]]]}))))

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

;; Empty rule placeholder used to seed the form with one blank row.
;; Must mirror the rule shape produced by rule-builder below (minus :_id,
;; which webapp.guardrails.helpers/format-rule generates when absent).
(def ^:private empty-rule
  {:type "" :rule "" :pattern_regex "" :words [] :message ""})

;; This event manages the state for when editing
;; or creating a new guardrails rule
(rf/reg-event-fx
 :guardrails->set-active-guardrail
 (fn
   [{:keys [db]} [_ rule]]
   (let [{:keys [id name description
                 connection_ids attributes
                 input output]} rule
         rule-builder (fn [rule]
                        {:_id (or (:_id rule) (random-uuid)) ;; internal use
                         :type (:type rule) ; :deny_words_list or :pattern
                         :words (:words rule)
                         :pattern_regex (:pattern_regex rule)
                         :message (or (:message rule) "")})
         rule-schema (merge
                      {:id (or id "")
                       :name (or name "")
                       :description (or description "")
                       :connection_ids (or connection_ids [])
                       :attributes (or attributes [])}
                      (if (seq (:rules input))
                        {:input (mapv rule-builder (:rules input))}
                        {:input [empty-rule]})
                      (if (seq (:rules output))
                        {:output (mapv rule-builder (:rules output))}
                        {:output [empty-rule]}))]
     {:db (assoc db :guardrails->active-guardrail
                 {:status :ready
                  :data (merge {:connections nil :connections-load-state {:loading false :remaining 0 :acc [] :errors []} :connections-error nil} rule-schema)})})))

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
                                              :attributes []
                                              :connections []
                                              :input [empty-rule]
                                              :output [empty-rule]}]]]}))

;; SUBSCRIPTIONS
(rf/reg-sub
 :guardrails->list
 (fn [db _]
   (get-in db [:guardrails->list])))

(rf/reg-sub
 :guardrails->active-guardrail
 (fn [db _]
   (get-in db [:guardrails->active-guardrail])))

(rf/reg-event-fx
 :guardrails/get-selected-connections
 (fn [{:keys [db]} [_ connection-ids]]
   (if (seq connection-ids)
     (let [page-size 30
           base-uri "/connections"
           ;; chunk ids into batches of page-size
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
                                                       (rf/dispatch [:guardrails/accumulate-selected-connections (:data response)]))
                                         :on-failure (fn [error]
                                                       (rf/dispatch [:guardrails/accumulate-selected-connections-error error]))}]])
                             chunks)]
       {:db (update-in db [:guardrails->active-guardrail :data] merge
                       {:connections-load-state {:loading true :remaining num-batches :acc [] :errors []}})
        :fx fx-requests})
     {:db (update-in db [:guardrails->active-guardrail :data] merge
                     {:connections [] :connections-load-state {:loading false :remaining 0 :acc [] :errors []}})})))

(rf/reg-event-fx
 :guardrails/accumulate-selected-connections
 (fn [{:keys [db]} [_ connections]]
   (let [{:keys [remaining acc errors]} (get-in db [:guardrails->active-guardrail :data :connections-load-state] {:loading false :remaining 0 :acc [] :errors []})
         new-remaining (dec remaining)
         new-acc (into acc connections)]
     (if (pos? new-remaining)
       {:db (update-in db [:guardrails->active-guardrail :data] merge
                       {:connections-load-state {:loading true
                                                 :remaining new-remaining
                                                 :acc new-acc
                                                 :errors errors}})}
       {:db (update-in db [:guardrails->active-guardrail :data] merge
                       {:connections-load-state {:loading false :remaining 0 :acc [] :errors []}})
        :fx [[:dispatch [:guardrails/set-selected-connections new-acc]]]}))))

(rf/reg-event-fx
 :guardrails/accumulate-selected-connections-error
 (fn [{:keys [db]} [_ error]]
   (let [{:keys [remaining acc errors]} (get-in db [:guardrails->active-guardrail :data :connections-load-state] {:loading false :remaining 0 :acc [] :errors []})
         new-remaining (dec remaining)
         new-errors (conj (vec errors) error)]
     (if (pos? new-remaining)
       {:db (update-in db [:guardrails->active-guardrail :data] merge
                       {:connections-load-state {:loading true
                                                 :remaining new-remaining
                                                 :acc acc
                                                 :errors new-errors}})}
       {:db (update-in db [:guardrails->active-guardrail :data] merge
                       {:connections-load-state {:loading false :remaining 0 :acc [] :errors []}})
        :fx [[:dispatch [:guardrails/set-selected-connections-error new-errors]]]}))))

(rf/reg-event-db
 :guardrails/set-selected-connections
 (fn [db [_ connections]]
   (let [filtered-connections (mapv #(select-keys % [:id :name]) connections)]
     (assoc-in db [:guardrails->active-guardrail :data :connections] filtered-connections))))

(rf/reg-event-db
 :guardrails/set-selected-connections-error
 (fn [db [_ error]]
   (update-in db [:guardrails->active-guardrail :data] merge
              {:connections [] :connections-error error})))
