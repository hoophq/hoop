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

;; True once :activation-journey/init has actually run (admin user). Lets a
;; surface that mounted before the current user finished loading re-dispatch
;; init exactly once instead of never fetching.
(rf/reg-sub
 :activation-journey/requested?
 :<- [:activation-journey/state]
 (fn [state _]
   (boolean (:requested? state))))

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

;; A feature counts as enabled for the current surface when any of its rules
;; already covers one of the surface's connections (guardrails/masking match
;; by connection id, the AI analyzer by connection name). With no connection
;; context there is nothing to match, so the feature reads as not enabled.
(defn- enabled-for-connections?
  [feature rules {:keys [connection-ids connection-names]}]
  (let [ids (set connection-ids)
        names (set connection-names)]
    (boolean
     (case feature
       (:guardrails :masking)
       (and (seq ids) (some #(some ids (:connection_ids %)) rules))

       :ai-analyzer
       (and (seq names) (some #(some names (:connection_names %)) rules))

       false))))

(defn- build-card
  "Decision matrix for one feature card. Evaluated in order:
  1. feature already enabled for this resource role -> done card, links back
  2. provider gate fails            -> CTA to the feature list page, no template
  3. free plan + feature configured -> locked card, Talk to Sales
  4. free plan, not configured      -> first recommended template
  5. enterprise                     -> next template not yet configured (by rule name)
  6. enterprise, all configured     -> generic copy, CTA to create page, no template"
  [{:keys [feature label generic-description]} feature-templates
   {:keys [free? gate-ok? enabled? configured? configured-names]}]
  (if enabled?
    {:feature feature
     :label label
     :enabled? true
     :badge {:label "Enabled" :color "green"}
     :title label
     :description "Already enabled for this resource role."
     :cta {:kind :list :label (str "Go to " label)}}
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
       :cta cta})))

;; The three feature cards, always ordered Guardrails -> Live Data Masking ->
;; AI Session Analyzer. `ctx` is {:subtype s :connection-ids [...]
;; :connection-names [...]}: the subtype scopes guardrail/analyzer templates
;; and the connections drive the per-role "already enabled" state.
(rf/reg-sub
 :activation-journey/cards
 :<- [:activation-journey/free-license?]
 :<- [:activation-journey/provider-gates]
 :<- [:activation-journey/feature-lists]
 (fn [[free? gates lists] [_ {:keys [subtype] :as ctx}]]
   (mapv (fn [{:keys [feature] :as info}]
           (let [data (get-in lists [feature :data])]
             (build-card info
                         (templates/templates-for feature subtype)
                         {:free? free?
                          :gate-ok? (boolean (get gates feature))
                          :enabled? (enabled-for-connections? feature data ctx)
                          :configured? (pos? (count data))
                          :configured-names (into #{} (map :name) data)})))
         templates/features)))

(rf/reg-sub
 :activation-journey/terminal-executed?
 :<- [:editor-plugin->script]
 (fn [script _]
   (contains? #{:success :running :failure} (:status script))))

(rf/reg-sub
 :activation-journey/terminal-banner-dismissed?
 :<- [:activation-journey/state]
 (fn [state _]
   (or (:terminal-banner-dismissed? state)
       (boolean (.getItem (.-localStorage js/window)
                          "activation-journey-terminal-banner-seen")))))

(defn- template-banner
  "Post-execution banner cycling through promotable feature templates in
  order (Guardrails -> Live Data Masking -> AI Session Analyzer), skipping
  provider-gated features and features already enabled for the current
  connection. On the free plan an org-configured feature is also skipped
  (its card is locked, so advertising an applicable template would lie); on
  enterprise each feature advances to the next template not yet configured."
  [free? gates lists {:keys [subtype] :as ctx} index]
  (let [candidates
        (->> templates/features
             (keep (fn [{:keys [feature label banner-cta]}]
                     (let [feature-templates (templates/templates-for feature subtype)
                           data (get-in lists [feature :data])
                           configured-names (into #{} (map :name) data)
                           configured? (pos? (count data))
                           template (if free?
                                      (when-not configured? (first feature-templates))
                                      (first (remove #(contains? configured-names (:name %))
                                                     feature-templates)))]
                       (when (and (get gates feature)
                                  (not (enabled-for-connections? feature data ctx))
                                  template)
                         {:variant :template
                          :feature feature
                          :label label
                          :banner-cta banner-cta
                          :title (:title template)
                          :description (:card-description template)
                          :template-id (templates/template-link-id template)}))))
             vec)]
    (when (seq candidates)
      (nth candidates (mod index (count candidates))))))

(defn- all-enabled-for-connections?
  [lists ctx]
  (every? (fn [{:keys [feature]}]
            (enabled-for-connections? feature (get-in lists [feature :data]) ctx))
          templates/features))

;; Terminal banner model, or nil when no banner applies. `ctx` is
;; {:subtype s :connection-ids [...] :connection-names [...]}.
;; - {:variant :unlock}   free-plan static "Unlock all protection controls"
;; - {:variant :template ...} cycling feature-template banner (+ :secondary)
;; - {:variant :protect}  enterprise generic "Protect your resource..."
(rf/reg-sub
 :activation-journey/terminal-banner
 (fn [_]
   [(rf/subscribe [:activation-journey/admin?])
    (rf/subscribe [:activation-journey/free-license?])
    (rf/subscribe [:activation-journey/terminal-executed?])
    (rf/subscribe [:activation-journey/terminal-banner-dismissed?])
    (rf/subscribe [:activation-journey/ready?])
    (rf/subscribe [:activation-journey/provider-gates])
    (rf/subscribe [:activation-journey/feature-lists])
    (rf/subscribe [:activation-journey/state])])
 (fn [[admin? free? executed? dismissed? ready? gates lists state] [_ ctx]]
   (let [index (:terminal-banner-index state 0)]
     (cond
       (not admin?)
       nil

       ;; Free plan: persistent banner before and after execution. Before the
       ;; first run (or while data is loading, or with nothing promotable)
       ;; show the static unlock banner; after a run cycle the templates.
       free?
       (if (and executed? ready?)
         (or (some-> (template-banner free? gates lists ctx index)
                     (assoc :secondary :see-all))
             {:variant :unlock})
         {:variant :unlock})

       ;; Enterprise: clean interface before execution; dismissible banner
       ;; after. When no template banner applies fall back to the generic
       ;; protect banner — unless every feature is already enabled for this
       ;; connection, in which case there is nothing left to promote.
       (or (not executed?) dismissed? (not ready?))
       nil

       :else
       (or (some-> (template-banner free? gates lists ctx index)
                   (assoc :secondary :not-now))
           (when-not (all-enabled-for-connections? lists ctx)
             {:variant :protect}))))))
