(ns webapp.features.activation-journey.subs
  (:require
   [re-frame.core :as rf]
   [webapp.features.activation-journey.templates :as templates]))

(rf/reg-sub
 :activation-journey/state
 (fn [db _]
   (get db :activation-journey {})))

(rf/reg-sub
 :activation-journey/free-license?
 :<- [:users->current-user]
 (fn [user _]
   (boolean (-> user :data :free-license?))))

(rf/reg-sub
 :activation-journey/admin?
 :<- [:users->current-user]
 (fn [user _]
   (boolean (-> user :data :admin?))))

(rf/reg-sub
 :activation-journey/masking-rules
 :<- [:activation-journey/state]
 (fn [state _]
   (:masking-rules state)))

;; Roles available for the just-created resource. We try, in order:
;; 1. roles fetched explicitly via GET /connections?name=<role-name> (works
;;    for both setup and add-role flows)
;; 2. last-created-roles (add-role flow: full connection responses with :id)
;; 3. last-created.connections / last-created.roles (when POST /resources
;;    returns roles inline)
(rf/reg-sub
 :activation-journey/roles-with-ids
 :<- [:activation-journey/state]
 :<- [:resources->last-created]
 :<- [:resources->last-created-roles]
 (fn [[state last-created last-created-roles] _]
   (let [fetched (:roles state)
         from-roles (filter :id last-created-roles)
         from-resource-connections (filter :id (:connections last-created))
         from-resource-roles (filter :id (:roles last-created))
         pick (first (filter seq [fetched from-roles from-resource-connections from-resource-roles]))]
     (mapv #(select-keys % [:id :name]) (or pick [])))))

;; Per-feature configured state derived from the org-wide rule lists.
(rf/reg-sub
 :activation-journey/feature-lists
 :<- [:guardrails->list]
 :<- [:activation-journey/masking-rules]
 :<- [:ai-session-analyzer/rules]
 (fn [[guardrails masking analyzer] _]
   {:guardrails {:status (:status guardrails) :data (vec (:data guardrails))}
    :masking {:status (:status masking) :data (vec (:data masking))}
    :ai-analyzer {:status (:status analyzer) :data (vec (:data analyzer))}}))

;; A feature CTA only deep-links into rule creation when the org can actually
;; enforce that feature; otherwise the card routes to the feature list page,
;; which owns the provider-required / promotion messaging.
(rf/reg-sub
 :activation-journey/provider-gates
 :<- [:gateway->info]
 :<- [:ai-session-analyzer/provider]
 (fn [[gateway-info provider] _]
   {:guardrails (boolean (-> gateway-info :data :has_redact_credentials))
    :masking (= "mspresidio" (-> gateway-info :data :redact_provider))
    :ai-analyzer (= :success (:status provider))}))

;; True once every fetch dispatched by :activation-journey/init has settled.
;; Errors count as settled so a failing endpoint degrades the surface instead
;; of leaving it in a permanent loading state.
(rf/reg-sub
 :activation-journey/ready?
 :<- [:activation-journey/feature-lists]
 :<- [:gateway->info]
 :<- [:ai-session-analyzer/provider]
 (fn [[lists gateway-info provider] _]
   (boolean
    (and (= :ready (get-in lists [:guardrails :status]))
         (contains? #{:success :error} (get-in lists [:masking :status]))
         (contains? #{:success :error} (get-in lists [:ai-analyzer :status]))
         (contains? #{:success :idle :error} (:status provider))
         (some? (:data gateway-info))))))

(defn- build-card
  "Decision matrix for one feature card. Evaluated in order:
  1. provider gate fails            -> CTA to the feature list page, no template
  2. free plan + feature configured -> locked card, Talk to Sales
  3. free plan, not configured      -> first recommended template
  4. enterprise                     -> next template not yet configured (by rule name)
  5. enterprise, all configured     -> generic copy, CTA to create page, no template"
  [{:keys [feature label generic-description]} feature-templates
   {:keys [free? gate-ok? configured? configured-names]}]
  (let [display-template (if free?
                           (first feature-templates)
                           (first (remove #(contains? configured-names (:name %))
                                          feature-templates)))
        locked? (and gate-ok? free? configured?)
        title (or (:title display-template) label)
        description (or (:card-description display-template) generic-description)
        cta (cond
              (not gate-ok?) {:kind :list :label (str "Go to " label)}
              locked? {:kind :talk-to-sales :label "Talk to Sales"}
              display-template {:kind :create-with-template
                                :label (str "Go to " label)
                                :template-id (templates/template-link-id display-template)}
              :else {:kind :create :label (str "Go to " label)})
        badge (cond
                locked? {:label "Upgrade to unlock" :color "gray"}
                free? {:label "Included in free plan" :color "green"}
                :else nil)]
    {:feature feature
     :label label
     :locked? locked?
     :badge badge
     :title title
     :description description
     :cta cta}))

;; The three feature cards, always ordered Guardrails -> Live Data Masking ->
;; AI Session Analyzer. `subtype` scopes the guardrail templates to the
;; resource/connection at hand.
(rf/reg-sub
 :activation-journey/cards
 :<- [:activation-journey/free-license?]
 :<- [:activation-journey/provider-gates]
 :<- [:activation-journey/feature-lists]
 (fn [[free? gates lists] [_ subtype]]
   (mapv (fn [{:keys [feature] :as info}]
           (let [data (get-in lists [feature :data])]
             (build-card info
                         (templates/templates-for feature subtype)
                         {:free? free?
                          :gate-ok? (boolean (get gates feature))
                          :configured? (pos? (count data))
                          :configured-names (into #{} (map :name) data)})))
         templates/features)))
