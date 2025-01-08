(ns webapp.auth.local.login
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
        password (r/atom "")
        loading (r/atom false)]
    (fn []
      [:> Box
       [:> Flex {:direction "column"}
        [forms/input {:label "Email"
                      :defaultValue @email
                      :type "email"
                      :on-change #(reset! email (-> % .-target .-value))}]
        [forms/input {:label "Password"
                      :defaultValue @password
                      :type "password"
                      :on-change #(reset! password (-> % .-target .-value))}]
        [:> Button {:color "indigo"
                    :size "2"
                    :disabled @loading
                    :onClick #(re-frame/dispatch [:localauth->login {:email @email
                                                                     :password @password}])
                    :variant "solid"
                    :radius "medium"}
         "Login"]
        [:> Flex {:align "center" :justify "center" :class "mt-4"}
         [:> Text {:as "div" :size "2" :color "gray-500"}
          "Don't have an account?"
          [:> Link {:href (routes/url-for :register-hoop) :class "text-blue-500 ml-1"}
           "Create one"]]]]])))

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
         "Login"]
        [form]]]]]))

