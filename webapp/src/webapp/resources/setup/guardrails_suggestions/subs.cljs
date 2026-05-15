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
                          :existing {}})))

(rf/reg-sub
 :guardrails-suggestions/list-for-subtype
 :<- [:resource-setup/resource-subtype]
 (fn [subtype _]
   (constants/for-subtype subtype)))

(rf/reg-sub
 :guardrails-suggestions/suggestion-state
 :<- [:guardrails-suggestions/state]
 (fn [{:keys [selected-toggles pending existing]} [_ suggestion-name]]
   {:selected-roles (get selected-toggles suggestion-name #{})
    :pending? (contains? pending suggestion-name)
    :existing-id (get existing suggestion-name)
    :checked? (some? (get existing suggestion-name))}))

;; Roles available for the just-created resource. We try, in order:
;; 1. last-created-roles (add-role flow: full connection responses with :id)
;; 2. last-created.connections (setup flow: roles embedded in resource response)
;; 3. last-created.roles (alternate shape from POST /resources)
(rf/reg-sub
 :guardrails-suggestions/roles-with-ids
 :<- [:resources->last-created]
 :<- [:resources->last-created-roles]
 (fn [[last-created last-created-roles] _]
   (let [from-roles (filter :id last-created-roles)
         from-resource-connections (filter :id (:connections last-created))
         from-resource-roles (filter :id (:roles last-created))
         pick (first (filter seq [from-roles from-resource-connections from-resource-roles]))]
     (mapv #(select-keys % [:id :name]) (or pick [])))))

(rf/reg-sub
 :guardrails-suggestions/all-role-ids
 :<- [:guardrails-suggestions/roles-with-ids]
 (fn [roles _]
   (mapv :id roles)))

;; Top 3 existing guardrails NOT already shown in the suggestions section,
;; ordered by number of connection_ids (most "used" first).
(rf/reg-sub
 :guardrails-suggestions/top-3-other
 :<- [:guardrails->list]
 :<- [:guardrails-suggestions/list-for-subtype]
 (fn [[guardrails-list suggestions] _]
   (let [data (or (:data guardrails-list) [])
         suggested-names (set (map :name suggestions))]
     (->> data
          (remove #(suggested-names (:name %)))
          (sort-by (fn [g] (- (count (:connection_ids g)))))
          (take 3)
          vec))))

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
