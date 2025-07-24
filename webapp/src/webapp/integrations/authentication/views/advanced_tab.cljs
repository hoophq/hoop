(ns webapp.integrations.authentication.views.advanced-tab
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Grid Heading Text]]
   ["lucide-react" :refer [AlertTriangle Copy RotateCw]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]))

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
        "Optional configuration that enables programmatic access with admin privileges."]]

      [:> Box {:class "space-y-radix-5" :grid-column "span 5 / span 5"}
       ;; Security notice
       [:> Callout.Root {:size "2" :variant "surface" :color "yellow"}
        [:> Callout.Icon
         [:> AlertTriangle {:size 16}]]
        [:> Callout.Text
         [:> Text {:weight "medium"} "Security notice: "]
         "This key has unrestricted access. Store securely and rotate regularly."]]

       ;; API Key fields
       [:> Box {:class "space-y-radix-4"}
        ;; Secret Key with actions
        [:> Box
         [:> Flex {:justify "between" :align "end" :mb "1"}
          [:> Text {:size "2" :weight "medium"} "Secret Key"]
          [:> Flex {:gap "2"}
           [:> Button {:size "1"
                       :variant "ghost"
                       :on-click #(copy-to-clipboard (:secret @api-key))}
            [:> Copy {:size 14}]
            "Copy"]
           [:> Button {:size "1"
                       :variant "ghost"
                       :on-click #(rf/dispatch [:authentication->generate-api-key])}
            [:> RotateCw {:size 14}]
            "Generate"]]]
         [forms/input
          {:value (:secret @api-key)
           :placeholder "e.g. VuOnc2nUwv8aCRhfQGsp"
           :class "font-mono"
           :on-change #(rf/dispatch [:authentication->update-advanced-field
                                     :api-key {:secret (-> % .-target .-value)}])}]]]

       ;; Learn more about API Keys
       [:> Text {:size "2" :class "text-blue-600 cursor-pointer hover:underline"}
        "Learn more about API Keys"]]]]))
