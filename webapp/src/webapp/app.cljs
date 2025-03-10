(ns webapp.app
  (:require
   ["@radix-ui/themes" :refer [Theme]]
   ["@sentry/browser" :as Sentry]
   ["gsap/all" :refer [Draggable gsap]]
   [bidi.bidi :as bidi]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.agents.new :as create-agent]
   [webapp.agents.panel :as agents]
   [webapp.audit.views.main :as audit]
   [webapp.audit.views.session-details :as session-details]
   [webapp.audit.views.sessions-filtered-by-id :as session-filtered-by-id]
   [webapp.auth.local.login :as local-auth-login]
   [webapp.auth.local.register :as local-auth-register]
   [webapp.auth.views.logout :as logout]
   [webapp.auth.views.signup :as signup]
   [webapp.components.dialog :as dialog]
   [webapp.components.draggable-card :as draggable-card]
   [webapp.components.headings :as h]
   [webapp.components.loaders :as loaders]
   [webapp.components.modal :as modals]
   [webapp.components.snackbar :as snackbar]
   [webapp.config :as config]
   [webapp.connections.views.connection-list :as connections]
   [webapp.connections.views.setup.connection-update-form :as connection-update-form]
   [webapp.connections.views.setup.events.db-events]
   [webapp.connections.views.setup.events.effects]
   [webapp.connections.views.setup.events.subs]
   [webapp.connections.views.setup.main :as connection-setup]
   [webapp.dashboard.main :as dashboard]
   [webapp.events]
   [webapp.events.agents]
   [webapp.events.ask-ai]
   [webapp.events.audit]
   [webapp.events.clarity]
   [webapp.events.components.dialog]
   [webapp.events.components.draggable-card]
   [webapp.events.components.modal]
   [webapp.events.components.sidebar]
   [webapp.events.connections]
   [webapp.events.database-schema]
   [webapp.events.editor-plugin]
   [webapp.events.gateway-info]
   [webapp.events.guardrails]
   [webapp.events.hoop-app]
   [webapp.events.indexer-plugin]
   [webapp.events.jira-integration]
   [webapp.events.jira-templates]
   [webapp.events.license]
   [webapp.events.localauth]
   [webapp.events.organization]
   [webapp.events.plugins]
   [webapp.events.reports]
   [webapp.events.reviews-plugin]
   [webapp.events.routes]
   [webapp.events.runbooks-plugin]
   [webapp.events.segment]
   [webapp.events.slack-plugin]
   [webapp.events.users]
   [webapp.guardrails.create-update-form :as guardrail-create-update]
   [webapp.guardrails.main :as guardrails]
   [webapp.jira-templates.create-update-form :as jira-templates-create-update]
   [webapp.jira-templates.main :as jira-templates]
   [webapp.onboarding.aws-connect :as aws-connect]
   [webapp.onboarding.events.aws-connect-events]
   [webapp.onboarding.events.effects]
   [webapp.onboarding.main :as onboarding]
   [webapp.onboarding.setup :as onboarding-setup]
   [webapp.onboarding.setup-resource :as onboarding-setup-resource]
   [webapp.organization.users.main :as org-users]
   [webapp.plugins.views.manage-plugin :as manage-plugin]
   [webapp.plugins.views.plugins-configurations :as plugins-configurations]
   [webapp.reviews.panel :as reviews]
   [webapp.reviews.review-detail :as review-detail]
   [webapp.routes :as routes]
   [webapp.settings.license.panel :as license-management]
   [webapp.shared-ui.sidebar.main :as sidebar]
   [webapp.slack.slack-new-organization :as slack-new-organization]
   [webapp.slack.slack-new-user :as slack-new-user]
   [webapp.subs :as subs]
   [webapp.upgrade-plan.main :as upgrade-plan]
   [webapp.views.home :as home]
   [webapp.webclient.events.codemirror]
   [webapp.webclient.events.connection-selection]
   [webapp.webclient.events.connections]
   [webapp.webclient.events.metadata]
   [webapp.webclient.events.multi-exec]
   [webapp.webclient.events.search]
   [webapp.webclient.panel :as webclient]))

(when (= config/release-type "hoop-ui")
  (js/window.addEventListener "load" (rf/dispatch [:segment->load])))

(defn auth-callback-panel-hoop
  "This panel works for receiving the token and storing in the session for later requests"
  []
  (let [search-string (.. js/window -location -search)
        url-params (new js/URLSearchParams search-string)
        token (.get url-params "token")
        error (.get url-params "error")
        redirect-after-auth (.getItem js/localStorage "redirect-after-auth")
        destiny (if error :login-hoop :onboarding)]
    (.removeItem js/localStorage "login_error")
    (when error (.setItem js/localStorage "login_error" error))
    (.setItem js/localStorage "jwt-token" token)
    (if (nil? redirect-after-auth)
      (rf/dispatch [:navigate destiny])
      (let [_ (.replace (. js/window -location) redirect-after-auth)
            _ (.removeItem js/localStorage "redirect-after-auth")]))

    [:div "Verifying authentication"
     [:span.w-16
      [:img.inline.animate-spin {:src (str config/webapp-url "/icons/icon-refresh.svg")}]]]))

(defn signup-callback-panel-hoop
  "This panel works for receiving the token and storing in the session for later requests"
  []
  (let [search-string (.. js/window -location -search)
        url-params (new js/URLSearchParams search-string)
        token (.get url-params "token")
        error (.get url-params "error")
        destiny (if error :login-hoop :signup-hoop)]
    (.removeItem js/localStorage "login_error")
    (when error (.setItem js/localStorage "login_error" error))
    (.setItem js/localStorage "jwt-token" token)
    (rf/dispatch [:navigate destiny])

    [:div "Verifying authentication"
     [:span.w-16
      [:img.inline.animate-spin {:src (str config/webapp-url "/icons/icon-refresh.svg")}]]]))

(defn- hoop-layout [_]
  (let [user (rf/subscribe [:users->current-user])]
    (rf/dispatch [:users->get-user])
    (rf/dispatch [:gateway->get-info])
    (fn [panels]
      (rf/dispatch [:routes->get-route])
      (rf/dispatch [:clarity->verify-environment (:data @user)])
      (rf/dispatch [:connections->connection-get-status])
      (if (empty? (:data @user))
        [loaders/over-page-loader]
        [:section
         {:class "antialiased min-h-screen"}
         [modals/modal]
         [modals/modal-radix]
         [dialog/dialog]
         [dialog/new-dialog]
         [snackbar/snackbar]
         [draggable-card/main]
         [sidebar/main panels]]))))

(defmulti layout identity)
(defmethod layout :application-hoop [_ panels]
  [hoop-layout panels])

(defmethod layout :auth [_ panels]
  [:<>
   (snackbar/snackbar)
   [modals/modal]
   [modals/modal-radix]
   panels])

;;;;;;;;;;;;;;;;;
;; HOOP PANELS ;;
;;;;;;;;;;;;;;;;;

(defmethod routes/panels :license-management-panel []
  [layout :application-hoop
   [:div {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
    [routes/wrap-admin-only
     [license-management/main]]]])

(defmethod routes/panels :agents-panel []
  [layout :application-hoop
   [:div {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
    [routes/wrap-admin-only
     [agents/main]]]])

(defmethod routes/panels :new-agent-panel []
  [layout :application-hoop
   [:div {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
    [routes/wrap-admin-only
     [create-agent/main]]]])

(defmethod routes/panels :home-panel []
  [layout :application-hoop [home/home-panel-hoop]])

(defmethod routes/panels :home-redirect-panel []
  [layout :application-hoop [home/home-panel-hoop]])

(defmethod routes/panels :onboarding-panel []
  [layout :auth [onboarding/main]])

(defmethod routes/panels :onboarding-aws-connect-panel []
  [layout :auth [aws-connect/main]])

(defmethod routes/panels :onboarding-setup-panel []
  [layout :auth [onboarding-setup/main]])

(defmethod routes/panels :onboarding-setup-resource-panel []
  [layout :auth [onboarding-setup-resource/main]])

(defmethod routes/panels :upgrade-plan-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop [:div {:class "bg-gray-1 min-h-full h-max"}
                             [upgrade-plan/main]]])

(defmethod routes/panels :users-panel []
  [layout :application-hoop [:div {:class "flex flex-col bg-gray-100 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
                             [routes/wrap-admin-only
                              [:<>
                               [h/h2 "Users" {:class "mb-6"}]
                               [org-users/main]]]]])

(defmethod routes/panels :connections-panel []
  [layout :application-hoop [:div {:class "flex flex-col bg-gray-100 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
                             [h/h2 "Connections" {:class "mb-6"}]
                             [connections/panel]]])

(defmethod routes/panels :dashboard-panel []
  [layout :application-hoop [:div {:class "flex flex-col bg-gray-100 px-4 py-10 sm:px-6 lg:px-12 lg:pt-16 lg:pb-10 h-full overflow-auto"}
                             [routes/wrap-admin-only
                              [:<>
                               [h/h2 "Dashboard" {:class "mb-6"}]
                               [dashboard/main]]]]])

(defmethod routes/panels :guardrails-panel []
  [layout :application-hoop
   [:div {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
    [routes/wrap-admin-only
     [guardrails/panel]]]])

(defmethod routes/panels :create-guardrail-panel []
  (rf/dispatch [:guardrails->clear-active-guardrail])
  [layout :application-hoop
   [:div {:class "bg-gray-1 min-h-full h-max relative"}
    [routes/wrap-admin-only
     [guardrail-create-update/main :create]]]])

(defmethod routes/panels :edit-guardrail-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        guardrail-id (:guardrail-id (:route-params current-route))]
    (rf/dispatch [:guardrails->get-by-id guardrail-id])
    [layout :application-hoop
     [:div {:class "bg-gray-1 min-h-full h-max relative"}
      [routes/wrap-admin-only
       [guardrail-create-update/main :edit]]]]))

(defmethod routes/panels :jira-templates-panel []
  [layout :application-hoop
   [:div {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
    [routes/wrap-admin-only
     [jira-templates/panel]]]])

(defmethod routes/panels :create-jira-template-panel []
  (rf/dispatch [:jira-templates->clear-active-template])
  [layout :application-hoop
   [:div {:class "bg-gray-1 min-h-full h-max relative"}
    [routes/wrap-admin-only
     [jira-templates-create-update/main :create]]]])

(defmethod routes/panels :edit-jira-template-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        guardrail-id (:jira-template-id (:route-params current-route))]
    (rf/dispatch [:jira-templates->get-by-id guardrail-id])
    [layout :application-hoop
     [:div {:class "bg-gray-1 min-h-full h-max relative"}
      [routes/wrap-admin-only
       [jira-templates-create-update/main :edit]]]]))

(defmethod routes/panels :editor-plugin-panel []
  (rf/dispatch [:destroy-page-loader])
  (rf/dispatch [:plugins->get-plugin-by-name "editor"])
  [layout :application-hoop [:div {:class "h-full"}
                             [webclient/main]]])

(defmethod routes/panels :reviews-plugin-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop [:div {:class "flex flex-col bg-gray-100 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
                             [:<>
                              [h/h2 "Reviews" {:class "mb-6"}]
                              [reviews/panel]]]])

(defmethod routes/panels :review-details-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        review-id (-> current-route :route-params :review-id)]
    (rf/dispatch [:destroy-page-loader])
    (rf/dispatch [:reviews-plugin->get-review-by-id {:id review-id}])
    [layout :application-hoop [:div {:class "bg-white p-large h-full"}
                               [review-detail/review-detail]]]))

(defmethod routes/panels :create-connection-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop [:div {:class "bg-gray-1 min-h-full h-full"}
                             [routes/wrap-admin-only
                              [connection-setup/main :create {}]]]])

(defmethod routes/panels :edit-connection-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        connection-name (:connection-name (:route-params current-route))]
    [layout :application-hoop [:div {:class "bg-[--gray-2] px-4 py-10 sm:px-6 lg:px-20 lg:pt-6 lg:pb-10 h-full overflow-auto"}
                               [routes/wrap-admin-only
                                [connection-update-form/main connection-name]]]]))

(defmethod routes/panels :manage-plugin-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        plugin-name (:plugin-name (:route-params current-route))]
    (rf/dispatch [:destroy-page-loader])
    (rf/dispatch [:plugins->get-plugin-by-name plugin-name])
    [layout :application-hoop
     [routes/wrap-admin-only
      [manage-plugin/main plugin-name]]]))

(defmethod routes/panels :manage-ask-ai-panel []
  (rf/dispatch [:destroy-page-loader])
  (layout :application-hoop [:div {:class "flex flex-col bg-gray-100 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
                             [routes/wrap-admin-only
                              [:<>
                               [h/h2 "AI Query Builder" {:class "mb-6"}]
                               [plugins-configurations/config "ask_ai"]]]]))

(defmethod routes/panels :manage-jira-panel []
  (rf/dispatch [:destroy-page-loader])
  (layout :application-hoop [:div {:class "flex flex-col bg-gray-100 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
                             [routes/wrap-admin-only
                              [:<>
                               [h/h2 "Jira" {:class "mb-6"}]
                               [plugins-configurations/config "jira"]]]]))

(defmethod routes/panels :audit-plugin-panel []
  ;; this performs a redirect while we're migrating
  ;; users to sessions instead of audit
  (rf/dispatch [:navigate :sessions]))

(defmethod routes/panels :sessions-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop [:div {:class "flex flex-col bg-gray-100 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full overflow-auto"}
                             [h/h2 "Sessions" {:class "mb-6"}]
                             [audit/panel]]])

(defmethod routes/panels :session-details-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        session-id (-> current-route :route-params :session-id)]
    (rf/dispatch [:destroy-page-loader])
    (rf/dispatch [:audit->get-session-details-page session-id])
    [layout :application-hoop [:div {:class "bg-white p-large h-full"}
                               [session-details/main]]]))

(defmethod routes/panels :sessions-list-filtered-by-ids-panel []
  (let [search (.. js/window -location -search)
        url-search-params (new js/URLSearchParams search)
        session-ids-param (.get url-search-params "id")
        session-id-list (if session-ids-param
                          (cs/split session-ids-param #",")
                          [])]
    (when (seq session-id-list)
      (rf/dispatch [:audit->get-filtered-sessions-by-id session-id-list]))
    (rf/dispatch [:destroy-page-loader])
    [layout :application-hoop [session-filtered-by-id/main]]))

(defmethod routes/panels :reviews-plugin-details-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        review-id (-> current-route :route-params :review-id)]
    (rf/dispatch [:reviews-plugin->get-review-by-id {:id review-id}])
    (rf/dispatch [:destroy-page-loader])
    [layout :application-hoop [review-detail/review-details-page]]))

(defmethod routes/panels :slack-new-organization-panel []
  [layout :application-hoop [slack-new-organization/main]])
(defmethod routes/panels :slack-new-user-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        slack-id (:slack-id (:route-params current-route))]
    [layout :application-hoop [slack-new-user/main slack-id]]))

(defmethod routes/panels :auth-callback-hoop-panel []
  [auth-callback-panel-hoop])

(defmethod routes/panels :signup-callback-hoop-panel []
  [signup-callback-panel-hoop])

(defmethod routes/panels :login-hoop-panel [_ gateway-info]
  (if (= (-> gateway-info :data :auth_method) "local")
    [layout :auth [local-auth-login/panel]]
    [layout :auth (rf/dispatch [:auth->get-auth-link])]))

(defmethod routes/panels :idplogin-hoop-panel []
  [layout :auth (rf/dispatch [:auth->get-auth-link {:prompt-login? true}])])

(defmethod routes/panels :register-hoop-panel [_ gateway-info]
  (if (= (-> gateway-info :data :auth_method) "local")
    [layout :auth [local-auth-register/panel]]
    [layout :auth (fn []
                    (rf/dispatch [:segment->track "SignUp - start signup"])
                    (rf/dispatch [:auth->get-signup-link]))]))

(defmethod routes/panels :signup-hoop-panel []
  [layout :auth [signup/panel]])

(defmethod routes/panels :logout-hoop-panel []
  [layout :auth [logout/main]])

;;;;;;;;;;;;;;;;;;;;;
;; END HOOP PANELS ;;
;;;;;;;;;;;;;;;;;;;;;

(defmethod routes/panels :default []
  [:div {:class "rounded-lg p-large bg-white"}
   [:header {:class "text-center"}
    [h/h1 "Page not found"]]
   [:footer {:class "text-center"}
    [:a {:href "/"
         :class "text-xs text-blue-500"}
     "Go to homepage"]]])

(defn sentry-monitor []
  (let [sentry-dsn config/sentry-dsn
        sentry-sample-rate config/sentry-sample-rate]
    (when (and sentry-dsn sentry-sample-rate)
      (.init Sentry #js {:dsn sentry-dsn
                         :release config/app-version
                         :sampleRate sentry-sample-rate
                         :integrations #js [(.browserTracingIntegration Sentry)]}))))

(defn main-panel []
  (let [active-panel (rf/subscribe [::subs/active-panel])
        gateway-public-info (rf/subscribe [:gateway->public-info])]
    (rf/dispatch [:gateway->get-public-info])
    (.registerPlugin gsap Draggable)
    (sentry-monitor)
    (fn []
      (when (not (-> @gateway-public-info :loading))
        [:> Theme {:radius "large" :panelBackground "solid"}
         [routes/panels @active-panel @gateway-public-info]]))))
