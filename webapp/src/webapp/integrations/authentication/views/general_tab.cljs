(ns webapp.integrations.authentication.views.general-tab
  (:require
   ["@radix-ui/themes" :refer [Box Grid Heading Text]]
   ["lucide-react" :refer [BookLock GlobeLock]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.callout-link :as callout-link]
   [webapp.components.selection-card :as selection-card]
   [webapp.config :as config]
   [webapp.integrations.authentication.views.providers.form-fields :as provider-forms]))

(defn icon-component [icon-path selected?]
  (r/as-element [:figure {:class "w-5"}
                 [:img {:class (when selected? "invert")
                        :src (str config/webapp-url icon-path)}]]))

(defn identity-provider-grid []
  (let [selected-provider (rf/subscribe [:authentication->selected-provider])]
    [:> Box
     [:> Grid {:columns "3" :gap "3" :mb "6"}
      (doall
       (for [provider [{:id "auth0" :name "Auth0" :icon [icon-component "/icons/identity-providers/auth0.svg"
                                                         (= @selected-provider "auth0")]}
                       {:id "aws-cognito" :name "AWS Cognito" :icon [icon-component "/icons/identity-providers/aws-cognito.svg"
                                                                     (= @selected-provider "aws-cognito")]}
                       {:id "azure" :name "Azure" :icon [icon-component "/icons/identity-providers/azure.svg"
                                                         (= @selected-provider "azure")]}
                       {:id "google" :name "Google" :icon [icon-component "/icons/identity-providers/google.svg"
                                                           (= @selected-provider "google")]}
                       {:id "jumpcloud" :name "JumpCloud" :icon [icon-component "/icons/identity-providers/jumpcloud.svg"
                                                                 (= @selected-provider "jumpcloud")]}
                       {:id "okta" :name "Okta" :icon [icon-component "/icons/identity-providers/okta.svg"
                                                       (= @selected-provider "okta")]}
                       {:id "other" :name "Other" :icon [:> GlobeLock {:size 20}]}]]
         ^{:key (:id provider)}
         [selection-card/selection-card
          {:icon (r/as-element (:icon provider))
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
        {:icon (r/as-element [:> BookLock {:size 18}])
         :title "Local authentication"
         :description "Create and manage accounts directly in Hoop"
         :selected? (= @auth-method "local")
         :on-click #(rf/dispatch [:authentication->set-auth-method "local"])}]

       ;; Identity Provider card
       [selection-card/selection-card
        {:icon (r/as-element [:> GlobeLock {:size 18}])
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
