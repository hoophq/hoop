(ns webapp.auth.local.register
  (:require
   ["@radix-ui/themes" :refer [Flex Card Heading
                               Link Box Text Button]]
   [webapp.components.forms :as forms]
   [reagent.core :as r]
   [re-frame.core :as re-frame]
   [webapp.config :as config]
   [webapp.routes :as routes]))

(defn- form []
  (let [email (r/atom "")
        fullname (r/atom "")
        password (r/atom "")
        confirm-password (r/atom "")
        loading (r/atom false)
        password-error (r/atom nil)
        submit-form (fn []
                      (reset! password-error nil)
                      (if (not= @password @confirm-password)
                        (reset! password-error "Passwords do not match")
                        (re-frame/dispatch [:localauth->register
                                            {:email @email
                                             :name @fullname
                                             :password @password}])))]
    (fn []
      [:> Box
       [:form {:on-submit (fn [e]
                            (js/event.preventDefault e)
                            (submit-form))}
        [:> Flex {:direction "column"}
         [forms/input {:label "Full name"
                       :defaultValue @fullname
                       :required true
                       :type "text"
                       :on-change #(reset! fullname (-> % .-target .-value))}]
         [forms/input {:label "Email"
                       :defaultValue @email
                       :required true
                       :type "email"
                       :on-change #(reset! email (-> % .-target .-value))}]
         [forms/input {:label "Password"
                       :required true
                       :defaultValue @password
                       :type "password"
                       :on-change #(reset! password (-> % .-target .-value))}]
         [forms/input {:label "Confirm Password"
                       :required true
                       :defaultValue @confirm-password
                       :type "password"
                       :on-blur #(if (not= @password @confirm-password)
                                   (reset! password-error "Passwords do not match")
                                   (reset! password-error nil))
                       :on-change #(reset! confirm-password (-> % .-target .-value))}]
         (when @password-error
           [:> Text {:size "1" :color "tomato" :mb "2"}
            @password-error])

         [:> Button {:color "indigo"
                     :size "2"
                     :disabled @loading
                     :type "submit"
                     :variant "solid"
                     :radius "medium"}
          "Register"]]]
       [:> Flex {:align "center" :justify "center" :class "mt-4"}
        [:> Text {:as "div" :size "2" :color "gray-500"}
         "Already have an account?"
         [:> Link {:href (routes/url-for :login-hoop) :class "text-blue-500 ml-1"}
          "Login"]]]])))

(defn panel []
  (fn []
    [:<>
     [:> Flex {:align "center"
               :justify "center"
               :height "100vh"
               :class "bg-gray-100"}
      [:> Box {:width "90%" :maxWidth "380px"}
       [:> Card {:size "4" :variant "surface" :class "bg-white"}
        [:img {:src (str config/webapp-url "/images/hoop-branding/SVG/hoop-symbol_black.svg")
               :class "w-12 mx-auto mb-6 mt-4"}]
        [:> Heading {:size "5" :align "center" :mb "5"}
         "Create an account"]
        [form]]]]]))

