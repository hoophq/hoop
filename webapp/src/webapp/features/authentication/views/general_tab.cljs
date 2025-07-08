(ns webapp.features.authentication.views.general-tab
  (:require
   ["@radix-ui/themes" :refer [Box Card Flex Grid Heading Switch Text]]
   ["lucide-react" :refer [Key Users]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.selection-card :as selection-card]
   [webapp.features.authentication.views.providers.form-fields :as provider-forms]))

(defn identity-provider-grid []
  (let [selected-provider (rf/subscribe [:authentication->selected-provider])]
    [:> Box
     ;; Provider selection grid
     [:> Grid {:columns "3" :gap "3" :mb "6"}
      ;; TODO: Replace with actual provider icons
      (for [provider [{:id "auth0" :name "Auth0"}
                      {:id "aws-cognito" :name "AWS Cognito"}
                      {:id "azure" :name "Azure"}
                      {:id "google" :name "Google"}
                      {:id "jumpcloud" :name "JumpCloud"}
                      {:id "okta" :name "Okta"}]]
        ^{:key (:id provider)}
        [:> Card {:class (str "cursor-pointer text-center p-4 border-2 transition-all "
                              (if (= @selected-provider (:id provider))
                                "border-blue-500 bg-blue-50"
                                "border-gray-200 hover:border-gray-300"))
                  :on-click #(rf/dispatch [:authentication->set-provider (:id provider)])}
         ;; Icon placeholder
         [:> Box {:class "w-12 h-12 bg-gray-200 rounded-lg mx-auto mb-2"}
          [:> Text {:size "1" :class "text-gray-500 flex items-center justify-center h-full"}
           "ICON"]]
         [:> Text {:size "2" :weight "medium"} (:name provider)]])]]))

(defn main []
  (let [auth-method (rf/subscribe [:authentication->auth-method])
        selected-provider (rf/subscribe [:authentication->selected-provider])
        advanced-config (rf/subscribe [:authentication->advanced-config])]
    [:> Box {:class "space-y-radix-9"}
     ;; Authentication Method section
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Authentication Method"]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "Choose how to authenticate and manage users."]]

      [:> Box {:class "space-y-radix-4" :grid-column "span 5 / span 5"}
       ;; Local authentication card
       [selection-card/selection-card
        {:icon (r/as-element [:> Key {:size 18}])
         :title "Local authentication"
         :description "Create and manage accounts directly in Hoop"
         :selected? (= @auth-method "local")
         :on-click #(rf/dispatch [:authentication->set-auth-method "local"])}]

       ;; Identity Provider card
       [selection-card/selection-card
        {:icon (r/as-element [:> Users {:size 18}])
         :title "Identity Provider"
         :description "Integrate with your existing SSO solution (Auth0, Okta, Google and more)"
         :selected? (= @auth-method "identity-provider")
         :on-click #(rf/dispatch [:authentication->set-auth-method "identity-provider"])}]

       ;; Learn more link
       [:> Text {:size "2" :class "text-blue-600 cursor-pointer hover:underline"}
        "Learn more about IDPs"]]]

     ;; Identity Provider Configuration (shown when selected)
     (when (= @auth-method "identity-provider")
       [:> Grid {:columns "7" :gap "7"}
        [:> Box {:grid-column "span 2 / span 2"}
         [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
          "Identity Provider"]
         [:> Text {:size "3" :class "text-[--gray-11]"}
          "Choose the identity provider that manages your organization's user accounts."]]

        [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
         [identity-provider-grid]

         ;; Provider-specific form
         (when @selected-provider
           [provider-forms/provider-form @selected-provider])]])

     ;; Hoop Local Authentication toggle
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Hoop Local Authentication"]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "Turn off Hoop native authentication to manage it only with Identity Providers."]]

      [:> Box {:class "space-y-radix-4" :grid-column "span 5 / span 5"}
       [:> Flex {:align "center" :gap "3"}
        [:> Switch {:checked (not= (:local-auth-enabled @advanced-config) false)
                    :onCheckedChange #(rf/dispatch [:authentication->toggle-local-auth %])}]
        [:> Text {:size "3" :weight "medium"}
         (if (not= (:local-auth-enabled @advanced-config) false) "On" "Off")]]

       [:> Text {:size "2" :class "text-[--gray-11]"}
        "When turned on, users list might be outdated until users sign up."]]]]))
