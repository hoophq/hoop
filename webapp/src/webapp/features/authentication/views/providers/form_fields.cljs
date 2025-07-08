(ns webapp.features.authentication.views.providers.form-fields
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Flex Text]]
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
        provider-fields (get-in provider-configs [provider :fields] [])]
    [:> Box
     ;; Common fields for all providers
     [:> Box {:class "space-y-radix-4"}
      ;; Client ID
      [:> Box
       [forms/input
        {:label "Client ID"
         :placeholder "OAuth application identifier from your identity provider."
         :value (:client-id @config)
         :on-change #(rf/dispatch [:authentication->update-config-field
                                   :client-id (-> % .-target .-value)])}]]

      ;; Client Secret
      [:> Box
       [forms/input
        {:label "Client Secret"
         :placeholder "Secret key from your OAuth identity provider."
         :type "password"
         :value (:client-secret @config)
         :on-change #(rf/dispatch [:authentication->update-config-field
                                   :client-secret (-> % .-target .-value)])}]]

      ;; Provider-specific fields
      (for [field provider-fields]
        ^{:key (:name field)}
        [:> Box
         [forms/input
          {:label (:label field)
           :placeholder (:placeholder field)
           :value (get @config (:name field))
           :on-change #(rf/dispatch [:authentication->update-config-field
                                     (:name field) (-> % .-target .-value)])}]
         (when (:description field)
           [:> Text {:size "2" :class "text-[--gray-11] mt-1"}
            (:description field)])])

      ;; Custom Scopes
      [:> Box
       [custom-scopes-input
        {:scopes (or (:custom-scopes @config) ["email" "profile"])
         :on-change #(rf/dispatch [:authentication->update-config-field :custom-scopes %])}]]]]))
