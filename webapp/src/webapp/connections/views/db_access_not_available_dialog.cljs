(ns webapp.connections.views.db-access-not-available-dialog
  (:require
   ["@radix-ui/themes" :refer [Button Heading]]
   [re-frame.core :as rf]))

(defn main
  "Dialog shown when database access method is not available"
  [{:keys [error-message user-is-admin?]}]

  [:section
   [:header {:class "mb-4"}
    [:> Heading {:size "6" :as "h2"}
     "Connection method not available"]]

   [:main {:class "space-y-4"}
    [:p {:class "text-sm text-gray-600 mb-4"}
     (or error-message
         "This connection method is not available at this moment. Please reach out to your organization admin to enable this method.")]

    (when user-is-admin?
      [:div {:class "bg-blue-50 border border-blue-200 rounded-lg p-3"}
       [:div {:class "text-blue-800 text-sm"}
        [:div {:class "font-medium mb-1"}
         "Development"]
        [:div {:class "text-xs"}
         "Feedback for non-admin users when:"]
        [:ul {:class "list-disc list-inside text-xs mt-1 space-y-1"}
         [:li "Default Proxy Port is not configured"]
         [:li "Reviews are enabled for this connection"]]]])]

   [:footer {:class "flex justify-end mt-6"}
    [:> Button
     {:variant "solid"
      :on-click #(rf/dispatch [:modal->close])}
     "Close"]]])
