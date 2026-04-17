(ns webapp.features.users.views.org-migration-dialog
  (:require
   ["@radix-ui/themes" :refer [AlertDialog Button Flex Text Strong]]
   [re-frame.core :as rf]))

(defn main []
  (let [invitations (rf/subscribe [:users/pending-org-invitations])]
    (fn []
      (when-let [invitation (first @invitations)]
        [:> AlertDialog.Root {:open true
                              :on-open-change (fn [open?]
                                               (when-not open?
                                                 (rf/dispatch [:users->decline-org-invitation (:org_id invitation)])))}
         [:> AlertDialog.Content {:size "3"
                                  :max-width "500px"}
          [:> AlertDialog.Title "Organization Invitation"]
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
            [:> Button {:on-click #(rf/dispatch [:users->accept-org-invitation (:org_id invitation)])}
             (str "Join " (:org_name invitation))]]]]]))))
