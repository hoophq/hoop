(ns webapp.auth.local.login
  (:require
    ["@radix-ui/themes" :refer [TextField Section Card
                                Button]]
    [reagent.core :as r]
    [re-frame.core :as re-frame]))

(defn panel []
  (let [email (r/atom "")
        password (r/atom "")
        loading (r/atom false)]
    (fn []
      (println :loading @loading)
      [:<>
       [:> Section
        [:> Card
         [:> TextField.Root {:placeholder "some@email.com"
                             :value @email
                             :type "email"
                             :onChange #(reset! email (-> % .-target .-value))}]
         [:> TextField.Root {:placeholder "Password"
                             :value @password
                             :type "password"
                             :onChange #(reset! password (-> % .-target .-value))}]
         [:> Button
          {:onClick #(re-frame/dispatch [:localauth->login {:email @email
                                                            :password @password}])}
          "Login"]]]])))

