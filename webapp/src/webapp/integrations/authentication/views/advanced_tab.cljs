(ns webapp.integrations.authentication.views.advanced-tab
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text]]
   ["lucide-react" :refer [Copy RotateCw]]
   [re-frame.core :as rf]
   [webapp.components.callout-link :as callout-link]
   [webapp.components.forms :as forms]
   [webapp.config :as config]))

(defn copy-to-clipboard [text]
  (-> js/navigator
      .-clipboard
      (.writeText text)
      (.then #(rf/dispatch [:show-snackbar {:level :success
                                            :text "Copied to clipboard!"}]))))

(defn main []
  (let [advanced-config (rf/subscribe [:authentication->advanced-config])
        api-key (rf/subscribe [:authentication->api-key])]
    [:> Box {:class "space-y-radix-9"}

     ;; Admin Role Name section
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Admin Role Name"]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "The group name that grants full administrative access to Hoop."]]

      [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
       [forms/input
        {:placeholder "admin"
         :value (or (:admin-role @advanced-config) "admin")
         :on-change #(rf/dispatch [:authentication->update-advanced-field
                                   :admin-role (-> % .-target .-value)])}]
       [:> Text {:size "2" :class "text-[--gray-11]"}
        "Members of this group can manage all settings and resources."]]]

     ;; Auditor Role Name section
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Auditor Role Name"]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "The group name for access to audit logs and session recordings."]]

      [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
       [forms/input
        {:placeholder "auditor"
         :value (or (:auditor-role @advanced-config) "auditor")
         :on-change #(rf/dispatch [:authentication->update-advanced-field
                                   :auditor-role (-> % .-target .-value)])}]
       [:> Text {:size "2" :class "text-[--gray-11]"}
        "Auditors can review activity but cannot modify settings."]]]

     ;; Service API Key section
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Service API Key"]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "Optional configuration that enables programmatic access with admin privileges."]

       [:br] [:br]

       [:> Text {:size "3" :weight "bold" :class "text-[--gray-11]"}
        "Security notice: "]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "This key has unrestricted access. Store securely and rotate regularly."]

       ;; Learn more link
       [callout-link/main {:href (get-in config/docs-url [:setup :apis :api-keys])
                           :text "Learn more about API Keys"}]]

      [:> Box {:class "space-y-radix-4" :grid-column "span 5 / span 5" :items "end"}
       [:> Flex {:gap "2"}
        [:> Flex {:direction "column" :gap "2" :width "100%"}
         [:> Flex {:justify "between" :align "center" :mb "1"}
          [:> Text {:size "2" :weight "medium"} "Secret Key"]
          [:> Flex {:gap "2"}
           [:> Button {:size "1"
                       :variant "ghost"
                       :color "gray"
                       :class "mr-1"
                       :on-click #(copy-to-clipboard (:secret @api-key))}
            [:> Copy {:size 14}]
            "Copy"]]]
         [:> Flex {:gap "2" :align "center" :items "center"}
          [forms/input
           {:value (:secret @api-key)
            :placeholder "e.g. VuOnc2nUwv8aCRhfQGsp"
            :full-width? true
            :not-margin-bottom? true
            :class "font-mono flex-1"
            :on-change #(rf/dispatch [:authentication->update-advanced-field
                                      :api-key {:secret (-> % .-target .-value)
                                                :newly-generated? (:newly-generated? @api-key)}])}]]]

        [:> Button {:size "3"
                    :variant "soft"
                    :color "gray"
                    :class "self-end"
                    :on-click #(rf/dispatch [:authentication->generate-api-key])}
         "Refresh"
         [:> RotateCw {:size 16}]]]]]]))
