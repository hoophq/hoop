(ns webapp.auth.local.login
  (:require
   ["@radix-ui/themes" :refer [TextField Section Card]]
   [reagent.core :as r]
   [re-frame.core :as re-frame]))

(defn panel []
  [:<>
   [:> Section
    [:> Card
     [:> TextField.Root {:placeholder "Email"}]
     [:> TextField.Root {:placeholder "Password"}]]]])

