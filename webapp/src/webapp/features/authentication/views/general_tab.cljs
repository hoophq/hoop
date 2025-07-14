(ns webapp.features.authentication.views.general-tab
  (:require
   ["@radix-ui/themes" :refer [Box Grid Heading Text]]
   ["lucide-react" :refer [Key Users]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.callout-link :as callout-link]
   [webapp.components.selection-card :as selection-card]
   [webapp.config :as config]
   [webapp.features.authentication.views.providers.form-fields :as provider-forms]))

(defn identity-provider-grid []
  (let [selected-provider (rf/subscribe [:authentication->selected-provider])]
    [:> Box
     [:> Grid {:columns "3" :gap "3" :mb "6"}
      ;; TODO: Replace with actual provider icons
      (doall
       (for [provider [{:id "auth0" :name "Auth0"}
                       {:id "aws-cognito" :name "AWS Cognito"}
                       {:id "azure" :name "Azure"}
                       {:id "google" :name "Google"}
                       {:id "jumpcloud" :name "JumpCloud"}
                       {:id "okta" :name "Okta"}]]
         ^{:key (:id provider)}
         [selection-card/selection-card
          {:icon (r/as-element [:> Users {:size 18}])
           :title (:name provider)
           :selected? (= @selected-provider (:id provider))
           :on-click #(rf/dispatch [:authentication->set-provider (:id provider)])}]))]]))

(defn main []
  (let [auth-method (rf/subscribe [:authentication->auth-method])
        selected-provider (rf/subscribe [:authentication->selected-provider])]
    [:> Box {:class "space-y-radix-9"}
     ;; Authentication Method section
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Authentication Method"]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "Choose how to authenticate and manage users."]

       [callout-link/main {:href (get-in config/docs-url [:setup :configuration :identity-providers])
                           :text "Learn more about IDPs"}]]

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
         :on-click #(rf/dispatch [:authentication->set-auth-method "identity-provider"])}]]]

     ;; Identity Provider Configuration (shown when selected)
     (when (= @auth-method "identity-provider")
       [:<>
        [:> Grid {:columns "7" :gap "7"}
         [:> Box {:grid-column "span 2 / span 2"}
          [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
           "Identity Provider"]
          [:> Text {:size "3" :class "text-[--gray-11]"}
           "Choose the identity provider that manages your organization's user accounts."]]

         [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
          [identity-provider-grid]]]

        (when @selected-provider
          [provider-forms/provider-form @selected-provider])])]))
