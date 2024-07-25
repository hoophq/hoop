(ns webapp.app
  (:require ["gsap/all" :refer [Draggable gsap]]
            [bidi.bidi :as bidi]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [webapp.audit.views.main :as audit]
            [webapp.audit.views.session-details :as session-details]
            [webapp.audit.views.sessions-filtered-by-id :as session-filtered-by-id]
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
            [webapp.connections.views.connection-connect :as connection-connect]
            [webapp.connections.views.connection-form-modal :as connection-form-modal]
            [webapp.connections.views.select-connection-use-cases :as select-connection-use-cases]
            [webapp.events]
            [webapp.events.agents]
            [webapp.events.ask-ai]
            [webapp.events.audit]
            [webapp.events.clarity]
            [webapp.events.components.dialog]
            [webapp.events.components.draggable-card]
            [webapp.events.components.sidebar]
            [webapp.events.connections]
            [webapp.events.editor-plugin]
            [webapp.events.gateway-info]
            [webapp.events.hoop-app]
            [webapp.events.indexer-plugin]
            [webapp.events.organization]
            [webapp.events.plugins]
            [webapp.events.reports]
            [webapp.events.reviews-plugin]
            [webapp.events.routes]
            [webapp.events.runbooks-plugin]
            [webapp.events.segment]
            [webapp.events.slack-plugin]
            [webapp.events.users]
            [webapp.organization.users.main :as org-users]
            [webapp.plugins.views.manage-plugin :as manage-plugin]
            [webapp.plugins.views.plugins-configurations :as plugins-configurations]
            [webapp.reviews.panel :as reviews]
            [webapp.reviews.review-detail :as review-details]
            [webapp.routes :as routes]
            [webapp.hoop-app.main :as hoop-app]
            [webapp.shared-ui.sidebar.main :as sidebar]
            [webapp.slack.slack-new-organization :as slack-new-organization]
            [webapp.slack.slack-new-user :as slack-new-user]
            [webapp.subs :as subs]
            [webapp.views.home :as home]
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
        destiny (if error :login-hoop :home)]
    (.removeItem js/localStorage "login_error")
    (when error (.setItem js/localStorage "login_error" error))
    (.setItem js/localStorage "jwt-token" token)
    (if (nil? redirect-after-auth)
      (rf/dispatch [:navigate destiny])
      (let [_ (.replace (. js/window -location) redirect-after-auth)
            _ (.removeItem js/localStorage "redirect-after-auth")]))

    [:div "Verifying authentication"
     [:span.w-16
      [:img.inline.animate-spin {:src "/icons/icon-refresh.svg"}]]]))

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
      [:img.inline.animate-spin {:src "/icons/icon-refresh.svg"}]]]))

(defn- hoop-layout [_]
  (let [user (rf/subscribe [:users->current-user])]
    (rf/dispatch [:users->get-user])
    (rf/dispatch [:gateway->get-info])
    (fn [panels]
      (rf/dispatch [:routes->get-route])
      (rf/dispatch [:clarity->verify-environment (:data @user)])
      (if (empty? (:data @user))
        [loaders/over-page-loader]
        [:section
         {:class "antialiased min-h-screen"}
         [modals/modal]
         [dialog/dialog]
         [dialog/new-dialog]
         [snackbar/snackbar]
         [draggable-card/main]
         [draggable-card/modal]
         [connection-connect/verify-connection-status]
         [sidebar/main panels]]))))

(defmulti layout identity)
(defmethod layout :application-hoop [_ panels]
  [hoop-layout panels])

(defmethod layout :auth [_ panels]
  (snackbar/snackbar)
  panels)

;;;;;;;;;;;;;;;;;
;; HOOP PANELS ;;
;;;;;;;;;;;;;;;;;


(defmethod routes/panels :home-panel []
  [layout :application-hoop [home/home-panel-hoop]])

(defmethod routes/panels :hoop-app-panel []
  [layout :application-hoop [:div {:class "flex flex-col bg-gray-100 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
                             [h/h2 "Hoop App" {:class "mb-6"}]
                             [hoop-app/main]]])

(defmethod routes/panels :users-panel []
  [layout :application-hoop [:div {:class "flex flex-col bg-gray-100 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
                             [h/h2 "Users" {:class "mb-6"}]
                             [org-users/main]]])

(defmethod routes/panels :connections-panel []
  [layout :application-hoop [:div {:class "flex flex-col bg-gray-100 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
                             [h/h2 "Connections" {:class "mb-6"}]
                             [connections/panel]]])

(defmethod routes/panels :editor-plugin-panel []
  (rf/dispatch [:destroy-page-loader])
  (rf/dispatch [:plugins->get-plugin-by-name "editor"])
  [layout :application-hoop [:div {:class "bg-gray-900 h-full"}
                             [webclient/main]]])

(defmethod routes/panels :reviews-plugin-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop [:div {:class "flex flex-col bg-gray-100 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
                             [h/h2 "Reviews" {:class "mb-6"}]
                             [reviews/panel]]])

(defmethod routes/panels :select-connection-use-cases-panel []
  (rf/dispatch [:destroy-page-loader])
  [layout :application-hoop [select-connection-use-cases/main]])

(defmethod routes/panels :create-connection-panel []
  (let [search (.. js/window -location -search)
        url-search-params (new js/URLSearchParams search)
        url-params-list (js->clj (for [q url-search-params] q))
        url-params-map (into (sorted-map) url-params-list)
        connection-type (get url-params-map "type")]
    (rf/dispatch [:destroy-page-loader])
    [layout :application-hoop [:div {:class "px-regular bg-white h-full"}
                               [connection-form-modal/main :create {} connection-type]]]))

(defmethod routes/panels :manage-plugin-panel []
  (let [pathname (.. js/window -location -pathname)
        current-route (bidi/match-route @routes/routes pathname)
        plugin-name (:plugin-name (:route-params current-route))]
    (rf/dispatch [:destroy-page-loader])
    (rf/dispatch [:plugins->get-plugin-by-name plugin-name])
    [layout :application-hoop [manage-plugin/main plugin-name]]))

(defmethod routes/panels :manage-ask-ai-panel []
  (rf/dispatch [:destroy-page-loader])
  (layout :application-hoop [:div {:class (str "h-full flex flex-col gap-small"
                                               " px-large py-regular bg-white")}
                             [:header {:class "flex mb-regular"}
                              [:div {:class "bg-gray-700 px-3 py-2 text-white rounded-lg"}
                               [:h1 {:class "text-2xl"}
                                "AI Query Builder"]]]
                             [plugins-configurations/config "ask_ai"]]))

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
    [layout :application-hoop [review-details/review-details-page]]))

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

(defmethod routes/panels :login-hoop-panel []
  [layout :auth (rf/dispatch [:auth->get-auth-link])])

(defmethod routes/panels :register-hoop-panel []
  [layout :auth (fn []
                  (rf/dispatch [:segment->track "SignUp - start signup"])
                  (rf/dispatch [:auth->get-signup-link]))])

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
   [:section {:class "flex justify-center items-center p-large"}
    [:figure {:class "overflow-hidden rounded-lg"}
     [:img {:src "https://media3.giphy.com/media/v1.Y2lkPTc5MGI3NjExbHIwd2QwY3o4M3A5bnk4MWx1dXd6OXc4bzlidnR5N2VrbzJ4YnlxZiZlcD12MV9pbnRlcm5hbF9naWZfYnlfaWQmY3Q9Zw/hEc4k5pN17GZq/giphy.gif"}]]]
   [:footer {:class "text-center"}
    [:a {:href "/"
         :class "text-xs text-blue-500"}
     "Go to homepage"]]])

(defn main-panel []
  (let [active-panel (rf/subscribe [::subs/active-panel])]
    (.registerPlugin gsap Draggable)
    [routes/panels @active-panel]))

