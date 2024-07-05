(ns webapp.auth.views.login-panel
  (:require
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.components.button :as button]
   [webapp.components.headings :as h]
   [webapp.events.auth]
   [webapp.components.divider :as divider]))

(defn submit []
  (rf/dispatch [:auth->get-auth-link]))

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
  (let [login-error (.getItem js/localStorage "login_error")]
    [:main {:class "h-screen grid grid-cols-1 lg:grid-cols-5"}
     [:div {:class "hidden lg:flex flex-col col-span-3 bg-gray-900 p-10 gap-small h-full"}
      [:figure {:class "w-48"}
       [:img {:src "/images/hoop-branding/PNG/hoop-symbol+text_white@4x.png"}]]
      [:figure {:class "mt-16 mb-10 self-center"}
       [:img {:src "/images/Illustration-1.png"}]]
      [:div {:class "self-center"}
       [:h1 {:class "text-4xl text-gray-900 font-semibold text-center bg-white rounded-lg p-2 mb-1"}
        "Security and Velocity"]
       [:h1 {:class "text-4xl text-white font-light text-center"}
        "to access your resources"]]]
     [:section {:class "col-span-2 flex flex-col items-center gap-8 mx-8 mt-72 lg:mx-24"}
      [:header {:class "grid place-items-center"}
       [:figure {:class "w-40 mb-regular"}
        [:img {:src "/images/hoop-branding/PNG/hoop-symbol+text_black@4x.png"}]]]
      [:form
       {:on-submit (fn [e]
                     (.preventDefault e)
                     (reset! loading (not @loading))
                     (submit))}
       (when login-error
         [:div {:class "pb-small"}
          [:span {:class "text-xs text-red-500"}
           (login-error-message login-error)]])
       [:div {:class "w-full"}
        [:header {:class "text-center mb-5"}
         [h/h4 "Sign in to your account"]]
        [:div {:class "flex flex-col gap-regular"}
         [button/black {:text "Sign In"
                        :status (when @loading :loading)
                        :full-width true
                        :type "submit"}]
         [divider/labeled "or"]
         [:span {:class "text-center text-gray-500 text-xs"}
          "Don't have an account?"
          [:a {:href "#"
               :class "text-blue-500"
               :on-click (fn []
                           (rf/dispatch [:segment->track "SignUp - start signup"])
                           (rf/dispatch [:auth->get-signup-link]))}
           " Sign Up"]]]]]]]))

