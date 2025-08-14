(ns webapp.app
  (:require
   ["@radix-ui/themes" :refer [Box Heading Spinner Theme]]
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
   [webapp.events.components.toast]
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
   [webapp.events.tracking]
   [webapp.events.users]
   [webapp.features.access-control.events]
   [webapp.features.access-control.main :as access-control]
   [webapp.features.access-control.subs]
   [webapp.features.access-control.views.group-form :as group-form]
   [webapp.features.runbooks.events]
   [webapp.features.runbooks.main :as runbooks]
   [webapp.features.runbooks.subs]
   [webapp.features.runbooks.views.runbook-form :as runbook-form]
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
   [webapp.onboarding.resource-providers :as onboarding-resource-providers]
   [webapp.onboarding.setup :as onboarding-setup]
   [webapp.onboarding.setup-resource :as onboarding-setup-resource]
   [webapp.onboarding.setup-agent :as onboarding-setup-agent]
   [webapp.plugins.views.manage-plugin :as manage-plugin]
   [webapp.reviews.panel :as reviews]
   [webapp.reviews.review-detail :as review-detail]
   [webapp.routes :as routes]
   [webapp.settings.infrastructure.events]
   [webapp.settings.infrastructure.main :as infrastructure]
   [webapp.settings.infrastructure.subs]
   [webapp.settings.license.panel :as license-management]
   [webapp.shared-ui.sidebar.main :as sidebar]
   [webapp.slack.slack-new-organization :as slack-new-organization]
   [webapp.slack.slack-new-user :as slack-new-user]
   [webapp.subs :as subs]
   [webapp.upgrade-plan.main :as upgrade-plan]
   [webapp.views.home :as home]
   [webapp.webclient.events.codemirror]
   [webapp.webclient.events.primary-connection]
   [webapp.webclient.events.multiple-connections]
   [webapp.webclient.events.multiple-connection-execution]
   [webapp.webclient.events.search]
   [webapp.webclient.events.metadata]
   [webapp.webclient.panel :as webclient]))

;; Tracking initialization is now handled by :tracking->initialize-if-allowed
;; which is dispatched after gateway info is loaded and checks do_not_track

(defn auth-callback-panel-hoop
  "This panel works for receiving the token and storing in the session for later requests"
  []
  (let [search-string (.. js/window -location -search)
        url-params (new js/URLSearchParams search-string)
        token (.get url-params "token")
        error (.get url-params "error")
        redirect-after-auth (.getItem js/localStorage "redirect-after-auth")]

    (.removeItem js/localStorage "login_error")
    (when error (.setItem js/localStorage "login_error" error))

    (.setItem js/localStorage "jwt-token" token)

    (if error
      (rf/dispatch [:navigate :login-hoop])

      (if (and redirect-after-auth (not (empty? redirect-after-auth)))
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
        token (.get url-params "token")
        error (.get url-params "error")
        destiny (if error :login-hoop :signup-hoop)]
    (.removeItem js/localStorage "login_error")
    (when error (.setItem js/localStorage "login_error" error))
    (.setItem js/localStorage "jwt-token" token)

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
  (let [user (rf/subscribe [:users->current-user])]
    (if (nil? (.getItem js/localStorage "jwt-token"))
      (do
        (let [current-url (.. js/window -location -href)]
          (.setItem js/localStorage "redirect-after-auth" current-url)
          (js/setTimeout #(rf/dispatch [:navigate :login-hoop]) 2000))
        [loading-transition])

      (do
        (rf/dispatch [:users->get-user])
        (rf/dispatch [:gateway->get-info])

        (fn [panels]
          (rf/dispatch [:routes->get-route])
          (rf/dispatch [:clarity->verify-environment (:data @user)])
          (rf/dispatch [:connections->connection-get-status])

          (cond
            (:loading @user)
            [loading-transition]

            (and (not (:loading @user))
                 (empty? (:data @user)))
            (do
              (let [current-url (.. js/window -location -href)]
                (.setItem js/localStorage "redirect-after-auth" current-url)
                (.removeItem js/localStorage "jwt-token")
                (js/setTimeout #(rf/dispatch [:navigate :login-hoop]) 2000))

              [loading-transition])

            :else
            [:section
             {:class "antialiased min-h-screen"}
             [:> Toaster {:position "top-right"}]
             [modals/modal]
             [modals/modal-radix]
             [dialog/dialog]
             [dialog/new-dialog]
             [snackbar/snackbar]
             [draggable-card/main]
             [sidebar/main panels]]))))))

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

(defmethod routes/panels :settings-infrastructure-panel []
  [layout :application-hoop
   [:div {:class "bg-gray-1 min-h-full h-full"}
    [routes/wrap-admin-only
     [routes/wrap-selfhosted-only
      [infrastructure/main]]]]])

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

(defmethod routes/panels :connections-panel []
  [layout :application-hoop [:> Box {:class "flex flex-col bg-gray-1 px-4 py-10 sm:px-6 lg:p-10 h-full space-y-radix-7"}
                             [:> Heading {:as "h1" :size "8" :weight "bold" :class "text-gray-12"}
                              "Connections"]
                             [connections/panel]]])

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
  (rf/dispatch [:plugins->get-plugin-by-name "editor"])
  [layout :application-hoop [:div {:class "h-full"}
                             [webclient/main]]])

(defmethod routes/panels :reviews-plugin-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop [:div {:class "flex flex-col bg-gray-1 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
                             [:<>
                              [h/h2 "Reviews" {:class "mb-6"}]
                              [reviews/panel]]]])

(defmethod routes/panels :review-details-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        review-id (-> current-route :route-params :review-id)]
    (rf/dispatch [:destroy-page-loader])
    (rf/dispatch [:reviews-plugin->get-review-details review-id])
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
  [layout :application-hoop [:div {:class "flex flex-col bg-gray-1 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full overflow-auto"}
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

(defmethod routes/panels :runbooks-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop
   [routes/wrap-admin-only
    [runbooks/main]]])

(defmethod routes/panels :runbooks-edit-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        connection-id (:connection-id (:route-params current-route))]
    (rf/dispatch [:destroy-page-loader])
    [layout :application-hoop
     [routes/wrap-admin-only
      [:div {:class "bg-gray-1 min-h-full h-max relative"}
       [runbook-form/main :edit {:connection-id connection-id}]]]]))

(defmethod routes/panels :runbooks-edit-path-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        path-id (:path-id (:route-params current-route))]
    (rf/dispatch [:destroy-page-loader])
    [layout :application-hoop
     [routes/wrap-admin-only
      [:div {:class "bg-gray-1 min-h-full h-max relative"}
       [runbook-form/main :edit {:path-id path-id}]]]]))

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
        analytics-tracking (rf/subscribe [:gateway->analytics-tracking])]
    (rf/dispatch [:gateway->get-public-info])
    (.registerPlugin gsap Draggable)
    (.registerModules ModuleRegistry #js[AllCommunityModule])

    (fn []
      (cond
        (-> @gateway-public-info :loading)
        [loading-transition]

        :else
        [:> Theme {:radius "large" :panelBackground "solid"}
         ;; Hidden element to display analytics_tracking value for testing
         [:div {:style {:display "none"}}
          [:span {:id "analytics-tracking-value"} (str @analytics-tracking)]]
         [routes/panels @active-panel @gateway-public-info]]))))
