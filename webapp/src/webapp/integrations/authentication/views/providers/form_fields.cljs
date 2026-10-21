(ns webapp.integrations.authentication.views.providers.form-fields
  (:require
   ["@radix-ui/themes" :refer [Box Flex Grid Heading Switch Text]]
   ["lucide-react" :refer [FileKey KeyRound]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]
   [webapp.components.selection-card :as selection-card]))

(defn array->select-options [array]
  (map #(into {} {"value" % "label" %}) array))

(defn- local-auth-toggle []
  (let [advanced-config (rf/subscribe [:authentication->advanced-config])]
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
       "When turned on, users list might be outdated until users sign up."]]]))

(defn- oidc-form []
  (let [config (rf/subscribe [:authentication->provider-config])]
    [:> Box {:class "space-y-radix-9"}
     ;; Client ID
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Client ID"]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "OAuth application identifier from your identity provider."]]

      [:> Box {:grid-column "span 5 / span 5"}
       [forms/input
        {:full-width? true
         :value (:client-id @config)
         :on-change #(rf/dispatch [:authentication->update-config-field
                                   :client-id (-> % .-target .-value)])}]]]

     ;; Client Authentication
     (let [federated? (= (:client-auth @config) "federated-token")]
       [:> Grid {:columns "7" :gap "7"}
        [:> Box {:grid-column "span 2 / span 2"}
         [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
          "Client Authentication"]
         [:> Text {:size "3" :class "text-[--gray-11]"}
          "How Hoop authenticates to your identity provider."]]

        [:> Box {:class "space-y-radix-4" :grid-column "span 5 / span 5"}
         [selection-card/selection-card
          {:icon (r/as-element [:> KeyRound {:size 18}])
           :title "Client secret"
           :description "A secret created in your identity provider"
           :selected? (not federated?)
           :on-click #(rf/dispatch [:authentication->set-client-auth "secret"])}]

         [selection-card/selection-card
          {:icon (r/as-element [:> FileKey {:size 18}])
           :title "Federated token"
           :description "A token file that your identity provider trusts, such as a Kubernetes service account token with an Entra ID federated credential. No secret to rotate."
           :selected? federated?
           :on-click #(rf/dispatch [:authentication->set-client-auth "federated-token"])}]

         (if federated?
           [forms/input
            {:full-width? true
             :not-margin-bottom? true
             :label "Token file"
             :helper-text "The path the gateway operator set in IDP_CLIENT_ASSERTION_FILE. The helm chart mounts the token here with idpFederatedToken.enabled."
             :placeholder "/var/run/secrets/hoop/idp/token"
             :value (:client-assertion-file @config)
             :on-change #(rf/dispatch [:authentication->update-config-field
                                       :client-assertion-file (-> % .-target .-value)])}]
           [forms/input
            {:full-width? true
             :not-margin-bottom? true
             :label "Client secret"
             :type "password"
             :value (:client-secret @config)
             :on-change #(rf/dispatch [:authentication->update-config-field
                                       :client-secret (-> % .-target .-value)])}])]])

     ;; Issuer URL
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Issuer URL"]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "The base URL where authentication endpoints are discovered."]]

      [:> Box {:grid-column "span 5 / span 5"}
       [forms/input
        {:full-width? true
         :placeholder "https://example.auth0.com/"
         :value (:issuer-url @config)
         :on-change #(rf/dispatch [:authentication->update-config-field
                                   :issuer-url (-> % .-target .-value)])}]]]

     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Custom Scopes"]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "Additional OAuth scopes to request during login."]]

      [:> Box {:grid-column "span 5 / span 5"}
       [multi-select/creatable-select
        {:label "Groups"
         :options (array->select-options ["email" "profile"])
         :default-value (array->select-options (:custom-scopes @config))
         :on-change (fn [value]
                      (rf/dispatch [:authentication->update-config-field
                                    :custom-scopes (mapv #(get % "value") (js->clj value))]))}]]]

     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Flex {:align "baseline" :gap "1"}
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "Audience"]
        [:> Text {:size "4" :weight "500" :class "text-[--gray-12]"}
         "(Optional)"]]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "API identifier for token validation (required by some providers like Auth0)."]]

      [:> Box {:grid-column "span 5 / span 5"}
       [forms/input
        {:full-width? true
         :placeholder "https://api.example.com"
         :value (:audience @config)
         :on-change #(rf/dispatch [:authentication->update-config-field
                                   :audience (-> % .-target .-value)])}]]]

     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Flex {:align "baseline" :gap "1"}
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "Groups Claim"]
        [:> Text {:size "4" :weight "500" :class "text-[--gray-12]"}
         "(Optional)"]]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "Specifies the claim identifier used to configure group propagation."]]

      [:> Box {:grid-column "span 5 / span 5"}
       [forms/input
        {:full-width? true
         :not-margin-bottom? true
         :placeholder "e.g. company-groups"
         :value (:groups-claim @config)
         :on-change #(rf/dispatch [:authentication->update-config-field
                                   :groups-claim (-> % .-target .-value)])}]
       [:> Text {:size "2" :class "text-[--gray-11]" :style {:margin-top "4px"}}
        "If not explicitly set, the system defaults to groups."]]]

     [local-auth-toggle]]))

(defn- saml-form []
  (let [saml-config (rf/subscribe [:authentication->saml-config])]
    [:> Box {:class "space-y-radix-9"}
     ;; IDP Metadata URL
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "IDP Metadata URL"]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "The URL of your identity provider's SAML metadata document."]]

      [:> Box {:grid-column "span 5 / span 5"}
       [forms/input
        {:full-width? true
         :placeholder "https://example.auth0.com/samlp/metadata/..."
         :value (:idp-metadata-url @saml-config)
         :on-change #(rf/dispatch [:authentication->update-saml-config-field
                                   :idp-metadata-url (-> % .-target .-value)])}]]]

     ;; Groups Claim
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:> Flex {:align "baseline" :gap "1"}
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "Groups Claim"]
        [:> Text {:size "4" :weight "500" :class "text-[--gray-12]"}
         "(Optional)"]]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "Specifies the claim identifier used to configure group propagation."]]

      [:> Box {:grid-column "span 5 / span 5"}
       [forms/input
        {:full-width? true
         :not-margin-bottom? true
         :placeholder "e.g. company-groups"
         :value (:groups-claim @saml-config)
         :on-change #(rf/dispatch [:authentication->update-saml-config-field
                                   :groups-claim (-> % .-target .-value)])}]
       [:> Text {:size "2" :class "text-[--gray-11]" :style {:margin-top "4px"}}
        "If not explicitly set, the system defaults to groups."]]]

     [local-auth-toggle]]))

(defn provider-form []
  (let [protocol (rf/subscribe [:authentication->protocol])]
    [:> Box
     (if (= @protocol "saml")
       [saml-form]
       [oidc-form])]))
