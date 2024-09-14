(ns webapp.auth.local.register
  (:require
    ["@radix-ui/themes" :refer [Flex Card Heading
                                Link Box Text]]
    [webapp.components.button :as button]
    [webapp.components.forms :as forms]
    [reagent.core :as r]
    [re-frame.core :as re-frame]))

(defn- form []
  (let [email (r/atom "")
        fullname (r/atom "")
        password (r/atom "")
        confirm-password (r/atom "")
        loading (r/atom false)]
    (fn []
      [:> Box
       [:> Flex {:direction "column"}
        [forms/input {:label "Full name"
                      :value @fullname
                      :type "text"
                      :on-change #(reset! fullname (-> % .-target .-value))}]
        [forms/input {:label "Email"
                      :value @email
                      :type "email"
                      :on-change #(reset! email (-> % .-target .-value))}]
        [forms/input {:label "Password"
                      :value @password
                      :type "password"
                      :on-change #(reset! password (-> % .-target .-value))}]
        [forms/input {:label "Confirm Password"
                      :value @confirm-password
                      :type "password"
                      :on-change #(reset! confirm-password (-> % .-target .-value))}]
        [button/tailwind-primary
         {:text "Register"
          :disabled @loading
          :on-click #(re-frame/dispatch [:localauth->register {:email @email
                                                              :name @fullname
                                                              :password @password}])}]
        [:> Flex {:align "center" :justify "center" :class "mt-4"}
         [:> Text {:as "div" :size "2" :color "gray-500"}
          "Already have an account?"
          [:> Link {:href "/login" :class "text-blue-500 ml-1"}
           "Login"]]
         ]]])))

(defn panel []
  (fn []
    [:<>
     [:> Flex {:align "center"
               :justify "center"
               :height "100vh"
               :class "bg-gray-100"}
      [:> Box {:width "90%" :maxWidth "380px"}
       [:> Card {:size "4" :variant "surface" :class "bg-white"}
        [:img {:src "/images/hoop-branding/SVG/hoop-symbol_black.svg"
               :class "w-12 mx-auto mb-6 mt-4"}]
        [:> Heading {:size "5" :align "center" :mb "5"}
         "Create an account"]
        [form]]]]]))

