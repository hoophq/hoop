(ns webapp.auth.views.signup
  (:require ["@heroicons/react/20/solid" :as hero-solid-icon]
            ["@radix-ui/themes" :refer [Box Heading Text]]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.forms :as forms]
            [webapp.components.loaders :as loaders]
            [webapp.events.auth]
            [webapp.config :as config]))

(defmulti ^:private login-error-message identity)
(defmethod ^:private login-error-message "slack_not_configured" [_]
  "You must configure your Slack with Hoop")
(defmethod ^:private login-error-message "code_exchange_failure" [_]
  "Something went wrong. Try again and if the error persist, talk to the account administrator")
(defmethod ^:private login-error-message "pending_review" [_]
  "The organization administrator must approve your access first")
(defmethod ^:private login-error-message "unregistered" [_]
  "Your user is not registered. Try to signup or talk to the account administrator")
(defmethod ^:private login-error-message :default [_ error] error)

(def loading (r/atom false))

(defn panel []
  (let [organization-name (r/atom "")
        user-name (r/atom "")
        userinfo (rf/subscribe [:users->current-user])]

    (when (empty? (:data @userinfo))
      (rf/dispatch [:users->get-user]))

    (fn []
      (let [login-error (.getItem js/localStorage "login_error")
            current-user (:data @userinfo)]
        [:main {:class "h-screen"}
         [:section {:class "h-full flex flex-col items-center justify-center"}
          (if (:loading @userinfo)
            [loaders/simple-loader]

            [:form
             {:on-submit (fn [e]
                           (.preventDefault e)
                           (reset! loading (not @loading))
                           (rf/dispatch [:segment->track "SignUp - Create organization"])
                           (rf/dispatch [:segment->identify (:data @userinfo)])
                           (rf/dispatch [:auth->signup @organization-name @user-name]))}
             (when login-error
               [:div {:class "pb-small"}
                [:span {:class "text-xs text-red-500"}
                 (login-error-message login-error)]])
             [:> Box {:class "spacey-radix-7"}
              [:> Box {:class "space-y-radix-6"}
               [:> Box
                [:img {:src (str config/webapp-url "/images/hoop-branding/PNG/hoop-symbol_black@4x.png")
                       :class "w-16 mx-auto py-4"}]]
               [:> Box
                [:> Heading {:as "h1" :align "center" :size "6" :mb "2" :weight "bold" :class "text-[--gray-12]"}
                 "Welcome to hoop.dev"]
                [:> Text {:as "p" :size "3" :align "center" :class "text-[--gray-12]"}
                 "Before getting started, set a name for your organization."]]]

              [:> Box {:class "space-y-radix-7"}
               [forms/input {:classes "whitespace-pre overflow-x"
                             :placeholder "Dunder Mifflin Inc"
                             :required true
                             :disabled (or (= "admin" (:role current-user))
                                           (= "standard" (:role current-user)))
                             :on-change #(reset! organization-name (-> % .-target .-value))
                             :value @organization-name}]
               (when (empty? (:name current-user))
                 [forms/input {:label "Your name"
                               :classes "whitespace-pre overflow-x"
                               :placeholder "Michael Scott"
                               :required true
                               :disabled (or (= "admin" (:role current-user))
                                             (= "standard" (:role current-user)))
                               :on-change #(reset! user-name (-> % .-target .-value))
                               :value @user-name}])
               [:div {:class "flex justify-center"}
                [button/primary {:text "Get started"
                                 :status (when @loading :loading)
                                 :disabled (or (= "admin" (:role current-user))
                                               (= "standard" (:role current-user)))
                                 :type "submit"}]]]]
             (when (= "admin" (:role current-user))
               [:div {:class "flex justify-end items-center gap-small mt-2 cursor-pointer text-gray-400 hover:text-blue-500"
                      :on-click (fn []
                                  (rf/dispatch [:segment->track "SignUp - Already registered"])
                                  (rf/dispatch [:navigate :login-hoop]))}
                [:> hero-solid-icon/ExclamationTriangleIcon {:class "h-6 w-6 shrink-0"
                                                             :aria-hidden "true"}]
                [:small
                 "You are already registered as admin, please click here and signin instead of signup."]])
             (when (= "standard" (:role current-user))
               [:div {:class "flex justify-end items-center gap-small mt-2 cursor-pointer text-gray-400 hover:text-blue-500"
                      :on-click (fn []
                                  (rf/dispatch [:segment->track "SignUp - Already registered"])
                                  (rf/dispatch [:navigate :login-hoop]))}
                [:> hero-solid-icon/ExclamationTriangleIcon {:class "h-6 w-6 shrink-0"
                                                             :aria-hidden "true"}]
                [:small
                 "You are already registered, please click here and signin instead of signup."]])])]]))))

