(ns webapp.onboarding.setup-resource
  (:require
   ["@radix-ui/themes" :refer [Box Button]]
   [re-frame.core :as rf]
   [webapp.connections.views.setup.main :as setup]))

(defn main []
  [:<>
   [:> Box {:class "p-radix-5 bg-[--gray-1] text-right w-full"}
    [:> Button {:variant "ghost"
                :size "2"
                :color "gray"
                :on-click #(rf/dispatch [:auth->logout])}
     "Logout"]]
   [setup/main :onboarding]])
