(ns webapp.resources.setup.guardrails-suggestions.subs
  (:require
   [re-frame.core :as rf]
   [webapp.resources.setup.guardrails-suggestions.constants :as constants]))

(def ^:private state-path [:resource-setup :guardrails-suggestions])

(rf/reg-sub
 :guardrails-suggestions/state
 (fn [db _]
   (get-in db state-path {:selected-toggles {}
                          :pending #{}
                          :existing {}
                          :open-items #{}
                          :original-ids {}
                          :new-selected {}
                          :initial-guardrails nil})))

(rf/reg-sub
 :guardrails-suggestions/open-items
 :<- [:guardrails-suggestions/state]
 (fn [state _]
   (vec (:open-items state #{}))))

;; Suggestions for the current subtype, filtered to exclude any that
;; the org already had as a guardrail at the start of this resource
;; flow. Driven by the snapshot in :initial-guardrails so a freshly
;; created suggestion stays put instead of jumping to "Your Guardrails".
(rf/reg-sub
 :guardrails-suggestions/list-for-subtype
 :<- [:resource-setup/resource-subtype]
 :<- [:guardrails-suggestions/state]
 (fn [[subtype state] _]
   (let [existing-names (->> (:initial-guardrails state)
                             (map :name)
                             set)]
     (->> (constants/for-subtype subtype)
          (remove #(existing-names (:name %)))
          vec))))

(rf/reg-sub
 :guardrails-suggestions/suggestion-state
 :<- [:guardrails-suggestions/state]
 (fn [{:keys [selected-toggles pending existing]} [_ suggestion-name]]
   {:selected-roles (get selected-toggles suggestion-name #{})
    :pending? (contains? pending suggestion-name)
    :existing-id (get existing suggestion-name)
    :checked? (some? (get existing suggestion-name))}))

;; Roles available for the just-created resource. We try, in order:
;; 1. roles fetched explicitly via GET /connections?name=<role-name> (works
;;    for both setup and add-role flows)
;; 2. last-created-roles (add-role flow: full connection responses with :id)
;; 3. last-created.connections / last-created.roles (when POST /resources
;;    returns roles inline)
(rf/reg-sub
 :guardrails-suggestions/roles-with-ids
 :<- [:guardrails-suggestions/state]
 :<- [:resources->last-created]
 :<- [:resources->last-created-roles]
 (fn [[state last-created last-created-roles] _]
   (let [fetched (:roles state)
         from-roles (filter :id last-created-roles)
         from-resource-connections (filter :id (:connections last-created))
         from-resource-roles (filter :id (:roles last-created))
         pick (first (filter seq [fetched from-roles from-resource-connections from-resource-roles]))]
     (mapv #(select-keys % [:id :name]) (or pick [])))))

;; Top 3 guardrails by connection_ids count from the initial snapshot
;; captured when this resource flow started. Reading from the snapshot
;; (not the live :guardrails->list) keeps the section stable while the
;; user creates/edits guardrails in this same flow; the next resource
;; setup re-snapshots and the list reflects the new state.
(rf/reg-sub
 :guardrails-suggestions/your-guardrails
 :<- [:guardrails-suggestions/state]
 (fn [state _]
   (->> (:initial-guardrails state)
        (sort-by (fn [g] (- (count (:connection_ids g)))))
        (take 3)
        vec)))

;; Per-card state for the "Your Guardrails" section. Drives the checkbox
;; (any new role marked = checked), the per-role Switch states, and the
;; pending flag while a PUT is in flight.
(rf/reg-sub
 :guardrails-suggestions/your-state
 :<- [:guardrails-suggestions/state]
 (fn [{:keys [new-selected pending]} [_ guardrail-id]]
   (let [selected (get new-selected guardrail-id #{})]
     {:selected-roles selected
      :checked? (boolean (seq selected))
      :pending? (contains? pending guardrail-id)})))

(rf/reg-sub
 :guardrails-suggestions/free-license?
 :<- [:users->current-user]
 (fn [user _]
   (-> user :data :free-license?)))

;; In free plan, only one guardrail total is allowed across the whole org.
(rf/reg-sub
 :guardrails-suggestions/limit-reached?
 :<- [:guardrails-suggestions/free-license?]
 :<- [:guardrails->list]
 (fn [[free? guardrails-list] _]
   (and free?
        (>= (count (:data guardrails-list)) 1))))
