(ns webapp.connections.views.db-access-not-available-dialog
  (:require
   ["@radix-ui/themes" :refer [Button Heading]]
   [re-frame.core :as rf]))

(defn main
  "Dialog shown when database access method is not available"
  [{:keys [error-message]}]

  [:section
   [:header {:class "mb-4"}
    [:> Heading {:size "6" :as "h2"}
     "Connection method not available"]]

   [:main {:class "space-y-4"}
    [:p {:class "text-sm text-gray-600 mb-4"}
     (or error-message
         "This connection method is not available at this moment. Please reach out to your organization admin to enable this method.")]]

   [:footer {:class "flex justify-end mt-6"}
    [:> Button
     {:variant "solid"
      :on-click #(rf/dispatch [:modal->close])}
     "Close"]]])
