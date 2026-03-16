(ns webapp.app
  (:require
   ["@radix-ui/themes" :refer [Box Heading Spinner]]
   ["ag-grid-community" :refer [AllCommunityModule ModuleRegistry]]
   ["gsap/all" :refer [Draggable gsap]]
   ["sonner" :refer [Toaster]]
   [bidi.bidi :as bidi]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.agents.new :as create-agent]
   [webapp.agents.panel :as agents]
   [webapp.ai-data-masking.create-update-form :as ai-data-masking-create-update]
   [webapp.ai-data-masking.events]
   [webapp.ai-data-masking.main :as ai-data-masking]
   [webapp.ai-data-masking.subs]
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
   [webapp.components.modal :as modals]
   [webapp.components.snackbar :as snackbar]
   [webapp.components.theme-provider :refer [theme-provider]]
   [webapp.shared-ui.cmdk.command-palette :as command-palette]
   [webapp.connections.views.setup.events.db-events]
   [webapp.connections.views.setup.events.effects]
   [webapp.connections.views.setup.events.subs]
   [webapp.dashboard.main :as dashboard]
   [webapp.connections.views.resource-catalog.main :as resource-catalog]
   [webapp.resources.setup.main :as resource-setup]
   [webapp.resources.setup.events.effects]
   [webapp.resources.setup.events.subs]
   [webapp.resources.main :as resources-main]
   [webapp.resources.configure.main :as resource-configure]
   [webapp.resources.configure-role.main :as configure-role]
   [webapp.resources.add-role.main :as add-role]
   [webapp.resources.add-role.events]
   [webapp.resources.subs]
   [webapp.resources.events]
   [webapp.events]
   [webapp.events.resources]
   [webapp.events.agents]
   [webapp.events.ask-ai]
   [webapp.events.audit]
   [webapp.events.clarity]
   [webapp.events.components.dialog]
   [webapp.events.components.draggable-card]
   [webapp.events.components.modal]
   [webapp.events.components.sidebar]
   [webapp.events.components.toast]
   [webapp.events.connections]
   [webapp.events.database-schema]
   [webapp.connections.native-client-access.events]
   [webapp.events.editor-plugin]
   [webapp.events.gateway-info]
   [webapp.events.clipboard]
   [webapp.events.guardrails]
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
   [webapp.events.segment]
   [webapp.events.slack-plugin]
   [webapp.events.tracking]
   [webapp.events.users]
   [webapp.shared-ui.cmdk.events.command-palette]
   [webapp.features.access-control.events]
   [webapp.features.access-control.main :as access-control]
   [webapp.features.access-control.subs]
   [webapp.features.access-control.views.group-form :as group-form]
   [webapp.features.access-request.events]
   [webapp.features.access-request.main :as access-request]
   [webapp.features.access-request.subs]
   [webapp.features.access-request.views.rule-form :as rule-form]
   [webapp.features.ai-session-analyzer.events]
   [webapp.features.ai-session-analyzer.main :as ai-session-analyzer]
   [webapp.features.ai-session-analyzer.subs]
   [webapp.features.ai-session-analyzer.views.rule-form :as ai-session-analyzer-rule-form]
   [webapp.features.attributes.events]
   [webapp.features.attributes.main :as attributes-main]
   [webapp.features.attributes.subs]
   [webapp.features.attributes.views.form :as attributes-form]
   [webapp.features.runbooks.setup.events]
   [webapp.features.runbooks.setup.main :as runbooks-setup]
   [webapp.features.runbooks.setup.subs]
   [webapp.features.runbooks.setup.views.runbook-rule-form :as runbook-rule-form]
   [webapp.features.runbooks.runner.events]
   [webapp.features.runbooks.runner.main :as runbooks-runner]
   [webapp.features.runbooks.runner.subs]
   [webapp.features.users.events]
   [webapp.features.users.main :as users]
   [webapp.features.users.subs]
   [webapp.guardrails.create-update-form :as guardrail-create-update]
   [webapp.guardrails.main :as guardrails]
   [webapp.integrations.aws-connect :as aws-connect-page]
   [webapp.integrations.authentication.events]
   [webapp.integrations.authentication.main :as integrations-authentication]
   [webapp.integrations.authentication.subs]
   [webapp.integrations.events]
   [webapp.integrations.jira.main :as jira-integration]
   [webapp.jira-templates.create-update-form :as jira-templates-create-update]
   [webapp.jira-templates.main :as jira-templates]
   [webapp.onboarding.aws-connect :as aws-connect]
   [webapp.onboarding.events.aws-connect-events]
   [webapp.onboarding.events.effects]
   [webapp.onboarding.main :as onboarding]
   [webapp.setup.main :as setup]
   [webapp.setup.events]
   [webapp.onboarding.resource-providers :as onboarding-resource-providers]
   [webapp.onboarding.setup :as onboarding-setup]
   [webapp.onboarding.setup-resource :as onboarding-setup-resource]
   [webapp.onboarding.setup-agent :as onboarding-setup-agent]
   [webapp.plugins.views.manage-plugin :as manage-plugin]
   [webapp.routes :as routes]
   [webapp.settings.api-keys.events]
   [webapp.settings.api-keys.main :as api-keys-main]
   [webapp.settings.api-keys.subs]
   [webapp.settings.api-keys.views.created :as api-keys-created]
   [webapp.settings.api-keys.views.form :as api-keys-form]
   [webapp.settings.experimental.events]
   [webapp.settings.experimental.main :as settings-experimental]
   [webapp.settings.experimental.subs]
   [webapp.settings.infrastructure.events]
   [webapp.settings.infrastructure.main :as infrastructure]
   [webapp.settings.infrastructure.subs]
   [webapp.settings.license.panel :as license-management]
   [webapp.audit-logs.main :as audit-logs]
   [webapp.shared-ui.sidebar.main :as sidebar]
   [webapp.slack.slack-new-organization :as slack-new-organization]
   [webapp.slack.slack-new-user :as slack-new-user]
   [webapp.subs :as subs]
   [webapp.upgrade-plan.main :as upgrade-plan]
   [webapp.utilities :refer [clear-cookie get-cookie-value]]
   [webapp.views.home :as home]
   [webapp.webclient.events.codemirror]
   [webapp.webclient.events.primary-connection]
   [webapp.webclient.events.search]
   [webapp.webclient.events.metadata]
   [webapp.webclient.panel :as webclient]))

(defn auth-callback-panel-hoop
  "This panel works for receiving the token and storing in the session for later requests"
  []
  (let [search-string (.. js/window -location -search)
        url-params (new js/URLSearchParams search-string)
        token (or (get-cookie-value "hoop_access_token")
                  (.get url-params "token"))
        error (.get url-params "error")
        redirect-after-auth (.getItem js/localStorage "redirect-after-auth")]

    (.removeItem js/localStorage "login_error")
    (when error (.setItem js/localStorage "login_error" error))

    (when token
      (.setItem js/localStorage "jwt-token" token)
      (clear-cookie "hoop_access_token"))

    (if error
      (rf/dispatch [:navigate :login-hoop])

      (if (and redirect-after-auth (seq redirect-after-auth))
        (js/setTimeout
         #(do
            (.removeItem js/localStorage "redirect-after-auth")
            (set! (.. js/window -location -href) redirect-after-auth))
         1500)

        (js/setTimeout
         #(rf/dispatch [:navigate :home])
         1500)))

    [:div {:class "min-h-screen bg-gray-100 flex items-center justify-center"}
     [:div {:class "bg-white rounded-lg shadow-md p-8 max-w-md w-full text-center"}
      [h/h2 "Verifying authentication..." {:class "mb-4"}]
      [:div {:class "flex justify-center"}
       [:> Spinner {:size "3"}]]]]))

(defn signup-callback-panel-hoop
  "This panel works for receiving the token and storing in the session for later requests"
  []
  (let [search-string (.. js/window -location -search)
        url-params (new js/URLSearchParams search-string)
        token (get-cookie-value "hoop_access_token")
        error (.get url-params "error")
        destiny (if error :login-hoop :signup-hoop)]
    (.removeItem js/localStorage "login_error")
    (when error (.setItem js/localStorage "login_error" error))

    ;; Store token from cookie in localStorage and clear the cookie for security
    (when token
      (.setItem js/localStorage "jwt-token" token)
      (clear-cookie "hoop_access_token"))

    (js/setTimeout
     #(rf/dispatch [:navigate destiny])
     1500)

    [:div {:class "min-h-screen bg-gray-100 flex items-center justify-center"}
     [:div {:class "bg-white rounded-lg shadow-md p-8 max-w-md w-full text-center"}
      [h/h2 "Verifying authentication..." {:class "mb-4"}]
      [:div {:class "flex justify-center"}
       [:> Spinner {:size "3"}]]]]))

(defn loading-transition []
  [:div {:class "min-h-screen bg-gray-100 flex items-center justify-center"}
   [:div {:class "bg-white rounded-lg shadow-md p-8 max-w-md w-full"}
    [:div {:class "text-center"}
     [h/h2 "Loading..." {:class "mb-4"}]
     [:div {:class "flex justify-center"}
      [:> Spinner {:size "3"}]]]]])

(defn- hoop-layout [_]
  (let [user (rf/subscribe [:users->current-user])
        react-shell? (boolean (.getItem js/localStorage "react-shell"))]
    (if (nil? (.getItem js/localStorage "jwt-token"))
      (do
        ;; React shell handles auth redirect — skip if in shell mode
        (when-not react-shell?
          (let [current-url (.. js/window -location -href)]
            (.setItem js/localStorage "redirect-after-auth" current-url)
            (js/setTimeout #(rf/dispatch [:navigate :login-hoop]) 2000)))
        [loading-transition])

      (do
        ;; In shell mode skip refetch if user data already exists (avoids loading flash on remount)
        (when-not (and react-shell? (-> @user :data some?))
          (rf/dispatch [:users->get-user])
          (rf/dispatch [:gateway->get-info]))

        (fn [panels]
          (rf/dispatch [:routes->get-route])
          (rf/dispatch [:clarity->verify-environment (:data @user)])
          (rf/dispatch [:native-client-access->cleanup-all-expired])
          (rf/dispatch [:native-client-access->check-active-sessions])

          (cond
            (:loading @user)
            [loading-transition]

            (and (not (:loading @user))
                 (empty? (:data @user)))
            (do
              ;; React shell handles invalid token via API interceptor
              (when-not react-shell?
                (let [current-url (.. js/window -location -href)]
                  (.setItem js/localStorage "redirect-after-auth" current-url)
                  (.removeItem js/localStorage "jwt-token")
                  (js/setTimeout #(rf/dispatch [:navigate :login-hoop]) 2000)))

              [loading-transition])

            :else
            (if react-shell?
              ;; Shell mode: React owns sidebar + cmdk, render only content + overlays
              [:section
               {:class "antialiased min-h-screen"}
               [:> Toaster {:position "top-right"}]
               [modals/modal]
               [modals/modal-radix]
               [dialog/dialog]
               [dialog/new-dialog]
               [snackbar/snackbar]
               [draggable-card/main]
               panels]
              ;; Normal mode: full layout with sidebar and cmdk
              [:section
               {:class "antialiased min-h-screen"}
               [:> Toaster {:position "top-right"}]
               [modals/modal]
               [modals/modal-radix]
               [dialog/dialog]
               [dialog/new-dialog]
               [snackbar/snackbar]
               [draggable-card/main]
               [command-palette/command-palette]
               [command-palette/keyboard-listener]
               [sidebar/main panels]])))))))

(defmulti layout identity)
(defmethod layout :application-hoop [_ panels]
  [hoop-layout panels])

(defmethod layout :auth [_ panels]
  [:<>
   [:> Toaster {:position "top-right"}]
   (snackbar/snackbar)
   [modals/modal]
   [modals/modal-radix]
   panels])

;;;;;;;;;;;;;;;;;
;; HOOP PANELS ;;
;;;;;;;;;;;;;;;;;

(defmethod routes/panels :license-management-panel []
  [layout :application-hoop
   [:div {:class "bg-gray-1 p-radix-7 min-h-full h-screen"}
    [routes/wrap-admin-only
     [license-management/main]]]])

(defmethod routes/panels :settings-experimental-panel []
  [layout :application-hoop
   [:div {:class "bg-gray-1 min-h-full h-full"}
    [routes/wrap-admin-only
     [settings-experimental/main]]]])

(defmethod routes/panels :settings-infrastructure-panel []
  [layout :application-hoop
   [:div {:class "bg-gray-1 min-h-full h-full"}
    [routes/wrap-admin-only
     [routes/wrap-selfhosted-only
      [infrastructure/main]]]]])

(defmethod routes/panels :settings-audit-logs-panel []
  [layout :application-hoop
   [:div {:class "bg-gray-1 min-h-full h-screen"}
    [routes/wrap-admin-only
     [audit-logs/main]]]])

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

(defmethod routes/panels :resource-catalog-panel []
  [layout :application-hoop [:> Box {:class "flex flex-col bg-gray-1 h-full space-y-radix-7"}
                             [resource-catalog/main]]])

(defmethod routes/panels :resource-setup-new-panel []
  ;; Initialize if not coming from catalog
  (when-not @(rf/subscribe [:resource-setup/from-catalog?])
    (rf/dispatch [:resource-setup->initialize-state nil]))
  [layout :application-hoop
   [:div {:class "bg-gray-1 min-h-full h-full"}
    [routes/wrap-admin-only
     [resource-setup/main]]]])

(defmethod routes/panels :home-panel []
  [layout :application-hoop [home/home-panel-hoop]])

(defmethod routes/panels :home-redirect-panel []
  [layout :application-hoop [home/home-panel-hoop]])

(defmethod routes/panels :setup-panel []
  [layout :auth [setup/main]])

(defmethod routes/panels :onboarding-panel []
  [layout :auth [onboarding/main]])

(defmethod routes/panels :onboarding-aws-connect-panel []
  [layout :auth [aws-connect/main :onboarding]])

(defmethod routes/panels :onboarding-setup-panel []
  [layout :auth [onboarding-setup/main]])

(defmethod routes/panels :onboarding-setup-resource-panel []
  [layout :auth [onboarding-setup-resource/main]])

(defmethod routes/panels :onboarding-setup-agent-panel []
  [layout :auth [onboarding-setup-agent/main]])

(defmethod routes/panels :onboarding-resource-providers-panel []
  [layout :auth [onboarding-resource-providers/main]])

(defmethod routes/panels :integrations-aws-connect-panel []
  [layout :application-hoop
   [:div {:class "flex flex-col bg-gray-100 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 overflow-auto h-full"}
    [routes/wrap-admin-only
     [:<>
      [h/h2 "AWS Connect" {:class "mb-6"}]
      [aws-connect-page/panel]]]]])

(defmethod routes/panels :integrations-aws-connect-setup-panel []
  (rf/dispatch [:aws-connect/initialize-state])
  (rf/dispatch [:connection-setup/set-type :aws-connect])

  [layout :application-hoop
   [:div {:class "bg-gray-1 min-h-full h-full"}
    [routes/wrap-admin-only
     [aws-connect/main :create]]]])

(defmethod routes/panels :integrations-authentication-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop
   [:div {:class "bg-gray-1 min-h-full h-full"}
    [routes/wrap-admin-only
     [routes/wrap-selfhosted-only
      [integrations-authentication/main]]]]])

(defmethod routes/panels :upgrade-plan-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop [:div {:class "bg-gray-1 min-h-full h-max"}
                             [upgrade-plan/main]]])

(defmethod routes/panels :users-panel []
  [layout :application-hoop
   [routes/wrap-admin-only
    [users/main]]])

(defmethod routes/panels :resources-panel []
  [layout :application-hoop
   [:> Box {:class "bg-gray-1 min-h-full h-screen"}
    [resources-main/panel]]])

(defmethod routes/panels :configure-resource-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        resource-id (:resource-id (:route-params current-route))]
    [layout :application-hoop
     [routes/wrap-admin-only
      [resource-configure/main resource-id]]]))

(defmethod routes/panels :configure-role-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        connection-name (:connection-name (:route-params current-route))]
    [layout :application-hoop
     [routes/wrap-admin-only
      [configure-role/main connection-name]]]))

(defmethod routes/panels :add-role-to-resource-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        resource-id (:resource-id (:route-params current-route))]
    [layout :application-hoop
     [routes/wrap-admin-only
      [add-role/main resource-id]]]))

(defmethod routes/panels :dashboard-panel []
  [layout :application-hoop [:div {:class "flex flex-col bg-gray-1 px-4 py-10 sm:px-6 lg:px-12 lg:pt-16 lg:pb-10 h-full overflow-auto"}
                             [routes/wrap-admin-only
                              [:<>
                               [h/h2 "Dashboard" {:class "mb-6"}]
                               [dashboard/main]]]]])

(defmethod routes/panels :guardrails-panel []
  [layout :application-hoop
   [routes/wrap-admin-only
    [guardrails/panel]]])

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
   [routes/wrap-admin-only
    [jira-templates/panel]]])

(defmethod routes/panels :create-jira-template-panel []
  (rf/dispatch [:jira-templates->clear-active-template])
  [layout :application-hoop
   [:div {:class "bg-gray-1 min-h-full h-max relative"}
    [routes/wrap-admin-only
     [jira-templates-create-update/main :create]]]])

(defmethod routes/panels :edit-jira-template-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        jira-template-id (:jira-template-id (:route-params current-route))]
    (rf/dispatch [:jira-templates->get-by-id jira-template-id])
    [layout :application-hoop
     [:div {:class "bg-gray-1 min-h-full h-max relative"}
      [routes/wrap-admin-only
       [jira-templates-create-update/main :edit]]]]))

(defmethod routes/panels :editor-plugin-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop [:div {:class "h-full"}
                             [webclient/main]]])

(defmethod routes/panels :runbooks-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop [:div {:class "h-full"}
                             [runbooks-runner/main]]])

(defmethod routes/panels :manage-plugin-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        plugin-name (:plugin-name (:route-params current-route))]
    (rf/dispatch [:destroy-page-loader])
    (rf/dispatch [:plugins->get-plugin-by-name plugin-name])
    [layout :application-hoop
     [routes/wrap-admin-only
      [manage-plugin/main plugin-name]]]))

(defmethod routes/panels :settings-jira-panel []
  (rf/dispatch [:destroy-page-loader])
  (layout :application-hoop [:div {:class "flex flex-col bg-gray-1 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
                             [routes/wrap-admin-only
                              [:<>
                               [h/h2 "Jira" {:class "mb-6"}]
                               [jira-integration/main]]]]))

(defmethod routes/panels :audit-plugin-panel []
  ;; this performs a redirect while we're migrating
  ;; users to sessions instead of audit
  (rf/dispatch [:navigate :sessions]))

(defmethod routes/panels :sessions-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop [:div {:class "flex flex-col bg-gray-1 px-4 py-10 sm:px-6 lg:p-10 h-full space-y-radix-7"}
                             [:> Heading {:as "h1" :size "8" :weight "bold" :class "text-gray-12"}
                              "Sessions"]
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
  (let [auth-method (-> gateway-info :data :auth_method)]
    (cond
      (= auth-method "local") [layout :auth [local-auth-login/panel]]
      (= auth-method "saml")  [layout :auth (rf/dispatch [:auth->get-saml-link])]
      :else                   [layout :auth (rf/dispatch [:auth->get-auth-link])])))

(defmethod routes/panels :idplogin-hoop-panel [_ gateway-info]
  (if (= (-> gateway-info :data :auth_method) "saml")
    [layout :auth (rf/dispatch [:auth->get-saml-link {:force-authn? true}])]
    [layout :auth (rf/dispatch [:auth->get-auth-link {:prompt-login? true}])]))

(defmethod routes/panels :register-hoop-panel [_ gateway-info]
  (let [auth-method (-> gateway-info :data :auth_method)]
    (cond
      (= auth-method "local") [layout :auth [local-auth-register/panel]]
      (= auth-method "saml")  [layout :auth (rf/dispatch [:auth->get-saml-link])]
      :else                   [layout :auth (fn []
                                              (rf/dispatch [:segment->track "SignUp - start signup"])
                                              (rf/dispatch [:auth->get-signup-link]))])))

(defmethod routes/panels :signup-hoop-panel []
  [layout :auth [signup/panel]])

(defmethod routes/panels :logout-hoop-panel []
  [layout :auth [logout/main]])

(defmethod routes/panels :access-control-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop
   [routes/wrap-admin-only
    [access-control/main]]])

(defmethod routes/panels :access-control-new-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop
   [routes/wrap-admin-only
    [:div {:class "bg-gray-1 min-h-full h-max relative"}
     [group-form/main :create]]]])

(defmethod routes/panels :access-control-edit-panel []
  (let [search (.. js/window -location -search)
        url-params (new js/URLSearchParams search)
        group-id (.get url-params "group")]
    (rf/dispatch [:destroy-page-loader])
    [layout :application-hoop
     [routes/wrap-admin-only
      [:div {:class "bg-gray-1 min-h-full h-max relative"}
       [group-form/main :edit {:group-id group-id}]]]]))

(defmethod routes/panels :access-request-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop
   [routes/wrap-admin-only
    [access-request/main]]])

(defmethod routes/panels :access-request-new-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop
   [routes/wrap-admin-only
    [:div {:class "bg-gray-1 min-h-full h-max relative"}
     [rule-form/main :create]]]])

(defmethod routes/panels :access-request-edit-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        rule-name (:rule-name (:route-params current-route))]
    (rf/dispatch [:destroy-page-loader])
    [layout :application-hoop
     [routes/wrap-admin-only
      [:div {:class "bg-gray-1 min-h-full h-max relative"}
       [rule-form/main :edit {:rule-name rule-name}]]]]))

(defmethod routes/panels :runbooks-setup-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop
   [routes/wrap-admin-only
    [runbooks-setup/main]]])

(defmethod routes/panels :create-runbooks-rule-panel []
  (rf/dispatch [:destroy-page-loader])
  (rf/dispatch [:runbooks-rules/clear-active-rule])
  [layout :application-hoop
   [:div {:class "bg-gray-1 min-h-full h-max relative"}
    [routes/wrap-admin-only
     [runbook-rule-form/main :create]]]])

(defmethod routes/panels :edit-runbooks-rule-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        rule-id (:rule-id (:route-params current-route))]
    (rf/dispatch [:destroy-page-loader])
    (rf/dispatch [:runbooks-rules/get-by-id rule-id])
    [layout :application-hoop
     [:div {:class "bg-gray-1 min-h-full h-max relative"}
      [routes/wrap-admin-only
       [runbook-rule-form/main :edit {:rule-id rule-id}]]]]))

(defmethod routes/panels :ai-data-masking-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop
   [routes/wrap-admin-only
    [ai-data-masking/main]]])

(defmethod routes/panels :create-ai-data-masking-panel []
  (rf/dispatch [:destroy-page-loader])
  (rf/dispatch [:ai-data-masking->clear-active-rule])
  [layout :application-hoop
   [:div {:class "bg-gray-1 min-h-full h-max relative"}
    [routes/wrap-admin-only
     [ai-data-masking-create-update/main :create]]]])

(defmethod routes/panels :edit-ai-data-masking-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        ai-data-masking-id (:ai-data-masking-id (:route-params current-route))]
    (rf/dispatch [:destroy-page-loader])
    (rf/dispatch [:ai-data-masking->get-by-id ai-data-masking-id])
    [layout :application-hoop
     [:div {:class "bg-gray-1 min-h-full h-max relative"}
      [routes/wrap-admin-only
       [ai-data-masking-create-update/main :edit]]]]))

(defmethod routes/panels :ai-session-analyzer-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop
   [routes/wrap-admin-only
    [ai-session-analyzer/main]]])

(defmethod routes/panels :create-ai-session-analyzer-rule-panel []
  (rf/dispatch [:destroy-page-loader])
  (rf/dispatch [:ai-session-analyzer/clear-active-rule])
  [layout :application-hoop
   [:div {:class "bg-gray-1 min-h-full h-max relative"}
    [routes/wrap-admin-only
     [ai-session-analyzer-rule-form/main :create]]]])

(defmethod routes/panels :edit-ai-session-analyzer-rule-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        rule-name (:rule-name (:route-params current-route))]
    (rf/dispatch [:destroy-page-loader])
    [layout :application-hoop
     [:div {:class "bg-gray-1 min-h-full h-max relative"}
      [routes/wrap-admin-only
       [ai-session-analyzer-rule-form/main :edit {:rule-name rule-name}]]]]))

(defmethod routes/panels :settings-attributes-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop
   [routes/wrap-admin-only
    [attributes-main/main]]])

(defmethod routes/panels :settings-attributes-new-panel []
  (rf/dispatch [:destroy-page-loader])
  (rf/dispatch [:attributes/clear-active])
  [layout :application-hoop
   [routes/wrap-admin-only
    [:div {:class "bg-gray-1 min-h-full h-max relative"}
     [attributes-form/main :create]]]])

(defmethod routes/panels :settings-attributes-edit-panel []
  (let [attr-name (-> js/window .-location .-search
                      js/URLSearchParams.
                      (.get "name"))]
    (rf/dispatch [:destroy-page-loader])
    (rf/dispatch [:attributes/get attr-name])
    [layout :application-hoop
     [routes/wrap-admin-only
      [:div {:class "bg-gray-1 min-h-full h-max relative"}
       [attributes-form/main :edit]]]]))

(defmethod routes/panels :settings-api-keys-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop
   [routes/wrap-admin-only
    [api-keys-main/main]]])

(defmethod routes/panels :settings-api-keys-new-panel []
  (rf/dispatch [:destroy-page-loader])
  (rf/dispatch [:api-keys/clear-active])
  [layout :application-hoop
   [routes/wrap-admin-only
    [:div {:class "bg-gray-1 min-h-full h-max relative"}
     [api-keys-form/main :create]]]])

(defmethod routes/panels :settings-api-keys-created-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop
   [routes/wrap-admin-only
    [api-keys-created/main]]])

(defmethod routes/panels :settings-api-keys-configure-panel []
  (let [id (-> (bidi/match-route @routes/routes (.. js/window -location -pathname))
               :route-params
               :id)]
    (rf/dispatch [:destroy-page-loader])
    (rf/dispatch [:api-keys/get id])
    [layout :application-hoop
     [routes/wrap-admin-only
      [:div {:class "bg-gray-1 min-h-full h-max relative"}
       [api-keys-form/main :configure]]]]))

;;;;;;;;;;;;;;;;;;;;;
;; END HOOP PANELS ;;
;;;;;;;;;;;;;;;;;;;;;

(defmethod routes/panels :default []
  (let [pathname (.. js/window -location -pathname)
        matched-route (try (bidi/match-route @routes/routes pathname) (catch js/Error _ nil))]

    (if (nil? matched-route)
      (do
        (js/setTimeout #(rf/dispatch [:navigate :home]) 5000)
        [:div {:class "min-h-screen bg-gray-100 flex items-center justify-center"}
         [:div {:class "bg-white rounded-lg shadow-md p-8 max-w-md w-full"}
          [:div {:class "text-center"}
           [h/h2 "Page not found" {:class "mb-4"}]
           [:p {:class "text-gray-600 mb-6"} "In a few seconds you will be redirected to the home page."]
           [:div {:class "flex justify-center"}
            [:> Spinner {:size "3"}]]]]])

      [loading-transition])))

(defn main-panel []
  (let [active-panel (rf/subscribe [::subs/active-panel])
        gateway-public-info (rf/subscribe [:gateway->public-info])
        react-shell? (boolean (.getItem js/localStorage "react-shell"))]
    ;; In shell mode skip refetch if public info already loaded (avoids loading flash on remount)
    (when-not (and react-shell? (-> @gateway-public-info :data some?))
      (rf/dispatch [:gateway->get-public-info]))
    (.registerPlugin gsap Draggable)
    (.registerModules ModuleRegistry #js[AllCommunityModule])

    (fn []
      (cond
        (-> @gateway-public-info :loading)
        [loading-transition]

        (and (-> @gateway-public-info :data :setup_required)
             (not= (-> @gateway-public-info :data :auth_method) "oidc")
             (not= @active-panel :setup-panel))
        (do
          (rf/dispatch [:navigate :setup])
          [loading-transition])

        :else
        [theme-provider
         [routes/panels @active-panel @gateway-public-info]]))))
