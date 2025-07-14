(ns webapp.features.authentication.views.providers.form-fields
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Flex Heading Grid Switch Text]]
   ["lucide-react" :refer [X]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

;; Provider-specific field configurations
(def provider-configs
  {"auth0" {:fields [{:name :audience
                      :label "Audience"
                      :placeholder "https://api.example.com"
                      :description "API identifier for token validation (required by some providers like Auth0)."
                      :optional true}]}
   "aws-cognito" {:fields []}
   "azure" {:fields []}
   "google" {:fields []}
   "jumpcloud" {:fields []}
   "okta" {:fields []}})

(defn custom-scopes-input [{:keys [scopes on-change]}]
  (let [input-value (r/atom "")]
    (fn [{:keys [scopes on-change]}]
      [:> Box
       [:> Text {:size "2" :weight "medium" :mb "1"} "Custom Scopes"]
       [:> Box {:class "flex flex-wrap gap-2 mb-2"}
        (for [scope scopes]
          ^{:key scope}
          [:> Badge {:variant "soft"
                     :size "2"
                     :class "pl-3 pr-1 py-1 flex items-center gap-1"}
           scope
           [:> Button {:size "1"
                       :variant "ghost"
                       :class "p-0 h-4 w-4"
                       :on-click #(on-change (vec (remove #{scope} scopes)))}
            [:> X {:size 12}]]])]
       [:> Flex {:gap "2"}
        [forms/input
         {:placeholder "email"
          :value @input-value
          :class "flex-1"
          :on-key-down (fn [e]
                         (when (= (.-key e) "Enter")
                           (.preventDefault e)
                           (when (not-empty @input-value)
                             (on-change (vec (conj scopes @input-value)))
                             (reset! input-value ""))))
          :on-change #(reset! input-value (-> % .-target .-value))}]
        [:> Button {:size "2"
                    :variant "soft"
                    :disabled (empty? @input-value)
                    :on-click (fn []
                                (when (not-empty @input-value)
                                  (on-change (vec (conj scopes @input-value)))
                                  (reset! input-value "")))}
         "Add"]]
       [:> Text {:size "2" :class "text-[--gray-11] mt-1"}
        "Additional OAuth scopes to request during login."]])))

(defn provider-form [provider]
  (let [config (rf/subscribe [:authentication->provider-config])
        advanced-config (rf/subscribe [:authentication->advanced-config])
        provider-fields (get-in provider-configs [provider :fields] [])]
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


      [:> Grid {:columns "7" :gap "7"}
       [:> Box {:grid-column "span 2 / span 2"}
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "Custom Scopes"]
        [:> Text {:size "3" :class "text-[--gray-11]"}
         "Additional OAuth scopes to request during login."]]

       [:> Box {:grid-column "span 5 / span 5"}
        [forms/input
         {:full-width? true
          :type "password"
          :value (:client-secret @config)
          :on-change #(rf/dispatch [:authentication->update-config-field
                                    :client-secret (-> % .-target .-value)])}]]]

      [:> Grid {:columns "7" :gap "7"}
       [:> Box {:grid-column "span 2 / span 2"}
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "Audience (Optional)"]
        [:> Text {:size "3" :class "text-[--gray-11]"}
         "API identifier for token validation (required by some providers like Auth0)."]]

       [:> Box {:grid-column "span 5 / span 5"}
        [forms/input
         {:full-width? true
          :type "password"
          :value (:client-secret @config)
          :on-change #(rf/dispatch [:authentication->update-config-field
                                    :client-secret (-> % .-target .-value)])}]]]

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
         "When turned on, users list might be outdated until users sign up."]]]

      ;; ;; Provider-specific fields
      ;; (for [field provider-fields]
      ;;   ^{:key (:name field)}
      ;;   [:> Box
      ;;    [forms/input
      ;;     {:label (:label field)
      ;;      :placeholder (:placeholder field)
      ;;      :value (get @config (:name field))
      ;;      :on-change #(rf/dispatch [:authentication->update-config-field
      ;;                                (:name field) (-> % .-target .-value)])}]
      ;;    (when (:description field)
      ;;      [:> Text {:size "2" :class "text-[--gray-11] mt-1"}
      ;;       (:description field)])])

      ;; Custom Scopes
      #_[:> Box
         [custom-scopes-input
          {:scopes (or (:custom-scopes @config) ["email" "profile"])
           :on-change #(rf/dispatch [:authentication->update-config-field :custom-scopes %])}]]]]))
