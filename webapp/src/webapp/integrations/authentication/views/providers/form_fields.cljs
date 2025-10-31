(ns webapp.integrations.authentication.views.providers.form-fields
  (:require
   ["@radix-ui/themes" :refer [Box Flex Grid Heading Switch Text]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]))

(defn array->select-options [array]
  (map #(into {} {"value" % "label" %}) array))


(defn provider-form []
  (let [config (rf/subscribe [:authentication->provider-config])
        advanced-config (rf/subscribe [:authentication->advanced-config])]
    [:> Box
     ;; Common fields for all providers
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

      ;; Client Secret
      [:> Grid {:columns "7" :gap "7"}
       [:> Box {:grid-column "span 2 / span 2"}
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "Client Secret"]
        [:> Text {:size "3" :class "text-[--gray-11]"}
         "Secret key from your OAuth identity provider."]]

       [:> Box {:grid-column "span 5 / span 5"}
        [forms/input
         {:full-width? true
          :type "password"
          :value (:client-secret @config)
          :on-change #(rf/dispatch [:authentication->update-config-field
                                    :client-secret (-> % .-target .-value)])}]]]

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
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "Audience (Optional)"]
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
         "When turned on, users list might be outdated until users sign up."]]]]]))
