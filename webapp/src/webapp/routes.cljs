(ns webapp.routes
  (:require
   [bidi.bidi :as bidi]
   [pushy.core :as pushy]
   [re-frame.core :as rf]
   [webapp.config :as config]
   [webapp.events :as events]))

(defmulti panels identity)
(def base-route
  "In production, the webapp base prefix path will replaced in runtime replacing the transpiled files."
  (if (= config/release-type "hoop-ui")
    "/hardcoded-runtime-prefix"
    ""))

(def routes
  (atom
   [base-route
    {"" :home-redirect
     "/" :home
     "/404" :404
     "/agents" [["" :agents]
                ["/new" :new-agent]]
     "/auth/callback" :auth-callback-hoop
     "/connections" [["" :connections]
                     ["/details" :connection-details]
                     ["/new" :create-connection]
                     [["/connections/" :connection-type "/new"] :onboarding-create-connection]
                     [["/edit/" :connection-name] :edit-connection]]
     "/client" :editor-plugin
     "/dashboard" :dashboard
     "/features" [["/access-control" :access-control]
                  ["/access-control/new" :access-control-new]
                  ["/access-control/edit" :access-control-edit]
                  ["/runbooks" :runbooks]
                  [["/runbooks/edit/" :connection-id] :runbooks-edit]
                  ["/data-masking" :ai-data-masking]
                  ["/data-masking/new" :create-ai-data-masking]
                  [["/data-masking/edit/" :ai-data-masking-id] :edit-ai-data-masking]]
     "/guardrails" [["" :guardrails]
                    ["/new" :create-guardrail]
                    [["/edit/" :guardrail-id] :edit-guardrail]]
     "/hoop-app" :hoop-app
     "/idplogin" :idplogin-hoop
     "/integrations" [["/aws-connect" :integrations-aws-connect]
                      ["/aws-connect/setup" :integrations-aws-connect-setup]
                      ["/authentication" :integrations-authentication]]
     "/jira-templates" [["" :jira-templates]
                        ["/new" :create-jira-template]
                        [["/edit/" :jira-template-id] :edit-jira-template]]
     "/login" :login-hoop
     "/logout" :logout-hoop
     "/plugins" [["/manage/ask-ai" :manage-ask-ai]
                 ["/manage/jira" :manage-jira]
                 [["/reviews/" :review-id] :reviews-plugin-details]
                 [["/manage/" :plugin-name] :manage-plugin]]
     "/organization" [["/users" :users]]
     "/onboarding" [["" :onboarding]
                    ["/aws-connect" :onboarding-aws-connect]
                    ["/setup" :onboarding-setup]
                    ["/setup/resource" :onboarding-setup-resource]
                    ["/resource-providers" :onboarding-resource-providers]]
     "/register" :register-hoop
     "/reviews" [["" :reviews-plugin]
                 [["/" :review-id] :review-details]]
     "/runbooks" [["" :runbooks-plugin]
                  [["/" :runbooks-file] :runbooks-plugin]]
     "/slack" [[["/user" "/new/" :slack-id] :slack-new-user]
               [["/organization" "/new"] :slack-new-organization]]
     "/sessions" [["" :sessions]
                  ["/filtered" :sessions-list-filtered-by-ids]
                  [["/" :session-id] :session-details]]
     "/settings" [["/license" :license-management]
                  ["/infrastructure" :settings-infrastructure]
                  ["/jira" :settings-jira]]
     "/signup" :signup-hoop
     "/signup/callback" :signup-callback-hoop
     "/upgrade-plan" :upgrade-plan}]))

(defn query-params-parser
  [queries]
  (let [url-search-params (new js/URLSearchParams (clj->js queries))]
    (if (not (empty? (.toString url-search-params)))
      (str "?" (.toString url-search-params))
      "")))

(defn parse
  [url]
  (try
    (bidi/match-route @routes url)
    (catch js/Error e
      {:handler :home})))

(defn url-for
  [& args]
  (apply bidi/path-for (into [@routes] (flatten args))))

(defn dispatch
  [route]
  (let [panel (keyword (str (name (:handler route)) "-panel"))]
    (rf/dispatch [::events/set-active-panel panel])))

(defonce history
  (pushy/pushy dispatch parse))

(defn navigate!
  [config]
  (let [uri (str (url-for (:handler config) (or (:params config) []))
                 (:query-params config))]
    (pushy/set-token! history uri)))

(defn start!
  []
  (pushy/start! history))

(rf/reg-fx
 :navigate
 (fn [config]
   (navigate! {:handler (:handler config)
               :params (:params config)
               :query-params (query-params-parser (:query-params config))})))

;; Subscription for checking if the user is an admin
(rf/reg-sub
 :user/is-admin?
 (fn [db]
   (get-in db [:users->current-user :data :admin?])))

;; Subscription for checking if the user has selfhosted tenancy type
(rf/reg-sub
 :user/is-selfhosted?
 (fn [db]
   (= "selfhosted" (get-in db [:users->current-user :data :tenancy_type]))))

;; Component wrapper to check if the user is an admin
;; If not, redirect to home and show a loader component
(defn admin-only []
  (let [is-admin? (rf/subscribe [:user/is-admin?])]
    (fn [component]
      (if (nil? @is-admin?)
        [:<>]
        (if @is-admin?
          ;; If it's an admin, render the component normally
          component
          ;; If it's not an admin, redirect to home and show a loader
          (do
            (js/setTimeout #(rf/dispatch [:navigate :home]) 1200)
            [:div {:class "flex items-center justify-center h-full"}
             [:div {:class "text-center"}
              [:div {:class "mb-4 text-xl font-medium text-gray-900"}
               "Redirecting..."]
              [:div {:class "text-sm text-gray-500"}
               "You don't have permission to access this page."]]]))))))

;; Function wrapper to wrap admin components
(defn wrap-admin-only [component]
  [admin-only component])

;; Component wrapper to check if the user has selfhosted tenancy type
;; If not, redirect to home and show a loader component
(defn selfhosted-only []
  (let [is-selfhosted? (rf/subscribe [:user/is-selfhosted?])]
    (fn [component]
      (if (nil? @is-selfhosted?)
        [:<>]
        (if @is-selfhosted?
          ;; If it's selfhosted, render the component normally
          component
          ;; If it's not selfhosted, redirect to home and show a loader
          (do
            (js/setTimeout #(rf/dispatch [:navigate :home]) 1200)
            [:div {:class "flex items-center justify-center h-full"}
             [:div {:class "text-center"}
              [:div {:class "mb-4 text-xl font-medium text-gray-900"}
               "Redirecting..."]
              [:div {:class "text-sm text-gray-500"}
               "This feature is only available for self-hosted environments."]]]))))))

;; Function wrapper to wrap selfhosted components
(defn wrap-selfhosted-only [component]
  [selfhosted-only component])

;; Example of usage:
;; Instead of:
;; (defmethod routes/panels :users-panel []
;;   [layout :application-hoop [...]])
;;
;; Use:
;; (defmethod routes/panels :users-panel []
;;   [layout :application-hoop [wrap-admin-only users/main]])
;;
;; Or for selfhosted-only:
;; (defmethod routes/panels :some-panel []
;;   [layout :application-hoop [wrap-selfhosted-only some-component/main]])

