(ns webapp.features.activation-journey.views.feature-cards
  (:require
   ["@radix-ui/themes" :refer [Grid]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.features.activation-journey.templates :as templates]
   [webapp.features.activation-journey.views.feature-card :as feature-card]
   [webapp.features.promotion :as promotion]))

(defn- feature-info [feature]
  (some #(when (= (:feature %) feature) %) templates/features))

(defn- dispatch-cta!
  "Resolves and fires the action of a feature card CTA.

  - :talk-to-sales opens the Intercom bubble (or hoop.dev/meet).
  - :list routes to the feature list page, which owns provider-required /
    promotion messaging.
  - :create / :create-with-template route to the feature create page; the
    template id and the connections of the current context ride along as
    query params (?template=&connections=) so the form opens pre-applied."
  [{:keys [feature cta] :as _card}
   {:keys [surface connection-ids connection-names on-navigate]}]
  (let [{:keys [route-create route-list connections-param]} (feature-info feature)
        connections (case connections-param
                      :ids connection-ids
                      :names connection-names
                      nil)
        csv (when (seq connections) (cs/join "," connections))
        navigate! (fn [event]
                    (when on-navigate (on-navigate))
                    (rf/dispatch event))]
    (rf/dispatch [:segment->track "Activation Journey - Feature CTA"
                  {:feature (name feature)
                   :surface (name (or surface :unknown))
                   :kind (name (:kind cta))}])
    ;; A user arriving from an activation CTA has already seen the feature
    ;; pitch; skip the AI analyzer first-visit promotion splash.
    (when (and (= feature :ai-analyzer)
               (not= (:kind cta) :talk-to-sales))
      (.setItem (.-localStorage js/window) "ai-session-analyzer-promotion-seen" "true"))
    (case (:kind cta)
      :talk-to-sales (promotion/request-demo)
      :list (navigate! [:navigate route-list])
      :create (navigate! [:navigate route-create])
      :create-with-template
      (navigate! [:navigate route-create
                  (cond-> {:template (:template-id cta)}
                    csv (assoc :connections csv))]))))

(defn main
  "The three activation-journey feature cards, always ordered Guardrails ->
  Live Data Masking -> AI Session Analyzer.

  Props:
  - :subtype           connection subtype scoping the guardrail templates
  - :surface           keyword identifying the host surface (analytics)
  - :with-roles?       resolve connections from the just-created resource roles
  - :connection-ids    explicit connection ids for the CTA deep links
  - :connection-names  explicit connection names (AI analyzer rules)
  - :on-navigate       called before any navigation (e.g. close the modal)

  Renders nothing until every backing fetch has settled, and nothing at all
  for non-admin users (the CTAs target admin-only pages)."
  [{:keys [with-roles?]}]
  (rf/dispatch [:activation-journey/init {:with-roles? with-roles?}])
  (fn [{:keys [subtype surface with-roles? connection-ids connection-names on-navigate]}]
    (let [admin? @(rf/subscribe [:activation-journey/admin?])
          ready? @(rf/subscribe [:activation-journey/ready?])
          cards @(rf/subscribe [:activation-journey/cards subtype])
          roles (when with-roles?
                  @(rf/subscribe [:activation-journey/roles-with-ids]))
          ctx {:surface surface
               :connection-ids (or connection-ids (mapv :id roles))
               :connection-names (or connection-names (mapv :name roles))
               :on-navigate on-navigate}]
      (when (and admin? ready?)
        [:> Grid {:columns "3" :gap "5"}
         (for [card cards]
           ^{:key (name (:feature card))}
           [feature-card/main card #(dispatch-cta! % ctx)])]))))
