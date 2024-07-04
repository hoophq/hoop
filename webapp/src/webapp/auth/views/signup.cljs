(ns webapp.auth.views.signup
  (:require ["@heroicons/react/20/solid" :as hero-solid-icon]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.forms :as forms]
            [webapp.components.headings :as h]
            [webapp.components.loaders :as loaders]
            [webapp.events.auth]))

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
    (rf/dispatch [:users->get-user])
    (fn []
      (let [login-error (.getItem js/localStorage "login_error")
            current-user (:data @userinfo)]
        [:main {:class "h-screen grid grid-cols-1 lg:grid-cols-5"}
         [:div {:class "hidden lg:flex flex-col col-span-3 bg-gray-900 p-10 gap-small max-h-screen over overflow-y-auto"}
          [:figure {:class "w-48"}
           [:img {:src "/images/hoop-branding/PNG/hoop-symbol+text_white@4x.png"}]]
          [:figure {:class "mt-16 mb-10 self-center"}
           [:img {:src "/images/Illustration-1.png"}]]
          [:div {:class "self-center"}
           [:h1 {:class "text-4xl text-gray-900 font-semibold text-center bg-white rounded-lg p-2 mb-1"}
            "Security and Velocity"]
           [:h1 {:class "text-4xl text-white font-light text-center"}
            "to access your resources"]]]
         [:section {:class "h-full col-span-2 flex flex-col items-center gap-8 mx-8 mt-72 lg:mx-24"}
          (if (:loading @userinfo)
            [loaders/simple-loader]

            [:form
             {:on-submit (fn [e]
                           (.preventDefault e)
                           (reset! loading (not @loading))
                           (rf/dispatch [:segment->track "SignUp - Create organization"])
                           (rf/dispatch [:auth->signup @organization-name @user-name]))}
             (when login-error
               [:div {:class "pb-small"}
                [:span {:class "text-xs text-red-500"}
                 (login-error-message login-error)]])
             [:div {:class "w-full"}
              [:header {:class "mb-8"}
               [h/h2 "Welcome to hoop.dev"
                {:class "text-3xl"}]
               [:label {:class "block text-sm mt-small"}
                "Before starting, please set a name for your organization."]]
              [forms/input {:label "Organization name"
                            :classes "whitespace-pre overflow-x"
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
              [:div {:class "flex justify-end"}
               [button/primary {:text "Get started"
                                :status (when @loading :loading)
                                :disabled (or (= "admin" (:role current-user))
                                              (= "standard" (:role current-user)))
                                :type "submit"}]]]
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

