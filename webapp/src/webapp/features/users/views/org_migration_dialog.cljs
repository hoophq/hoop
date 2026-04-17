(ns webapp.features.users.views.org-migration-dialog
  (:require
   ["@radix-ui/themes" :refer [AlertDialog Button Flex Text Strong]]
   ["lucide-react" :refer [Building2 Loader2]]
   [re-frame.core :as rf]
   [reagent.core :as r]))

(defn- migration-loading [org-name]
  [:> Flex {:direction "column" :align "center" :justify "center" :gap "4" :py "8"}
   [:> Loader2 {:size 40 :class "animate-spin text-blue-600"}]
   [:> Flex {:direction "column" :align "center" :gap "1"}
    [:> Text {:size "4" :weight "bold" :align "center"}
     "Migrating your organization"]
    [:> Text {:size "2" :color "gray" :align "center"}
     "Setting up your account in "
     [:> Strong org-name]
     ". This may take a moment."]]])

(defn main []
  (let [invitations (rf/subscribe [:users/pending-org-invitations])
        migrating? (r/atom false)]
    (fn []
      (when-let [invitation (first @invitations)]
        [:> AlertDialog.Root {:open true}
         [:> AlertDialog.Content {:size "3" :max-width "500px"}
          (if @migrating?
            [migration-loading (:org_name invitation)]
            [:<>
             [:> Flex {:align "center" :gap "2" :mb "1"}
              [:> Building2 {:size 20 :class "text-gray-11"}]
              [:> AlertDialog.Title "Organization Invitation"]]
             [:> AlertDialog.Description {:size "2"}
              [:> Text
               "You have been invited to join "
               [:> Strong (:org_name invitation)]
               ". Would you like to migrate to this organization? "
               "Your current organization will be removed."]]
             [:> Flex {:gap "3" :mt "4" :justify "end"}
              [:> AlertDialog.Cancel
               [:> Button {:color "gray"
                           :highContrast true
                           :variant "soft"
                           :on-click #(rf/dispatch [:users->decline-org-invitation (:org_id invitation)])}
                "Stay in current org"]]
              [:> AlertDialog.Action
               [:> Button {:on-click (fn []
                                       (reset! migrating? true)
                                       (rf/dispatch [:users->accept-org-invitation
                                                     (:org_id invitation)
                                                     #(reset! migrating? false)]))}
                (str "Join " (:org_name invitation))]]]])]]))))
