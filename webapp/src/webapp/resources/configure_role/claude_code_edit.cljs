(ns webapp.resources.configure-role.claude-code-edit
  (:require
   ["@radix-ui/themes" :refer [Box Callout Flex Heading Switch Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.configuration-inputs :as configuration-inputs]
   [webapp.connections.views.setup.connection-method :as connection-method]))

(defn- credential-value
  "Reads a claude-code credential field tolerating both the {:value ...} map
  shape (used when the value is sourced from a secrets manager) and a plain
  string. Returns default-value when the field is absent/blank."
  [credentials k default-value]
  (let [v (get credentials k)]
    (cond
      (map? v) (or (:value v) default-value)
      (and (string? v) (not= v "")) v
      :else default-value)))

(defn claude-code-edit-form []
  (let [credentials (rf/subscribe [:connection-setup/claude-code-credentials])
        connection-method (rf/subscribe [:connection-setup/connection-method])
        env-vars (rf/subscribe [:connection-setup/environment-variables])
        vertex-flag? (rf/subscribe [:feature-flag/enabled? "experimental.claude_code_vertex"])]
    (fn []
      (r/with-let
        [initialized? (atom false)]

        (let [show-selector? (= @connection-method "secrets-manager")
              all-env-vars @env-vars
              x-api-key-env (some #(when (= (:key %) "X_API_KEY") %) all-env-vars)
              provider (credential-value @credentials :provider "anthropic")
              vertex? (= provider "vertex")
              ;; The Vertex option is shown when the feature is enabled, or when
              ;; an already-Vertex connection is being edited on a gateway where
              ;; the flag happens to be off, so the form never hides existing
              ;; configuration.
              show-vertex-option? (or @vertex-flag? vertex?)
              remote-url-value (credential-value @credentials :remote_url "https://api.anthropic.com")
              api-key-value (or (credential-value @credentials :HEADER_X_API_KEY nil)
                                (when x-api-key-env
                                  (if (map? (:value x-api-key-env))
                                    (:value (:value x-api-key-env))
                                    (:value x-api-key-env)))
                                "")
              project-value (credential-value @credentials :GCP_PROJECT_ID "")
              region-value (credential-value @credentials :GCP_REGION "us-east5")
              sa-json-value (credential-value @credentials :GCP_SERVICE_ACCOUNT_JSON "")
              insecure-value (let [raw-insecure (:insecure @credentials)]
                               (cond
                                 (boolean? raw-insecure) raw-insecure
                                 (map? raw-insecure) (boolean (:value raw-insecure))
                                 (string? raw-insecure) (= raw-insecure "true")
                                 :else false))]

          ;; Initialize once: move X_API_KEY to credentials and filter from env-vars
          (when (and (not @initialized?) x-api-key-env)
            (reset! initialized? true)
            ;; Move to credentials if it doesn't exist
            (when (empty? (or (:HEADER_X_API_KEY @credentials) ""))
              (rf/dispatch [:connection-setup/update-claude-code-credentials
                            "HEADER_X_API_KEY"
                            (if (map? (:value x-api-key-env))
                              (:value (:value x-api-key-env))
                              (:value x-api-key-env))]))
            ;; Clear from env-vars
            (let [filtered (filterv #(not= (:key %) "X_API_KEY") all-env-vars)]
              (rf/dispatch [:connection-setup/set-env-vars filtered])))

          [:form
           {:id "credentials-form"
            :on-submit (fn [e] (.preventDefault e))}

           [:> Box {:class "space-y-radix-6"}
            [connection-method/main "claude-code"]

            [:> Box {:class "space-y-radix-4"}
             [:> Heading {:size "4" :weight "bold"} "Basic info"]

             (when show-vertex-option?
               [forms/select
                {:label "Provider"
                 :options [{:text "Anthropic API" :value "anthropic"}
                           {:text "Google Vertex AI" :value "vertex"}]
                 :selected provider
                 :on-change #(rf/dispatch [:connection-setup/update-claude-code-provider %])}])

             (if vertex?
               [:<>
                [:> Callout.Root {:size "1" :color "gray"}
                 [:> Callout.Text
                  "Claude Code runs in Vertex mode against hoop. hoop mints a short-lived "
                  "token from the service account below and proxies requests to Google Vertex AI."]]

                [forms/input
                 {:label "GCP Region"
                  :placeholder "us-east5"
                  :required true
                  :value region-value
                  :on-change #(rf/dispatch [:connection-setup/update-claude-code-credentials
                                            "GCP_REGION"
                                            (-> % .-target .-value)])}]

                [forms/input
                 {:label "GCP Project ID"
                  :placeholder "my-gcp-project"
                  :required true
                  :value project-value
                  :on-change #(rf/dispatch [:connection-setup/update-claude-code-credentials
                                            "GCP_PROJECT_ID"
                                            (-> % .-target .-value)])}]

                [forms/textarea
                 {:label "Service Account JSON"
                  :placeholder "{\n  \"type\": \"service_account\",\n  ...\n}"
                  :required true
                  :rows 8
                  :value sa-json-value
                  :on-change #(rf/dispatch [:connection-setup/update-claude-code-credentials
                                            "GCP_SERVICE_ACCOUNT_JSON"
                                            (-> % .-target .-value)])}]]

               [:<>
                [forms/input
                 {:label "Anthropic API URL"
                  :placeholder "https://api.anthropic.com"
                  :required true
                  :value remote-url-value
                  :on-change #(rf/dispatch [:connection-setup/update-claude-code-credentials
                                            "remote_url"
                                            (-> % .-target .-value)])
                  :start-adornment (when show-selector?
                                     [connection-method/source-selector "remote_url"])}]

                [forms/input
                 {:label "Anthropic API Key"
                  :placeholder "sk-ant-..."
                  :required true
                  :type "password"
                  :value api-key-value
                  :on-change #(rf/dispatch [:connection-setup/update-claude-code-credentials
                                            "HEADER_X_API_KEY"
                                            (-> % .-target .-value)])
                  :start-adornment (when show-selector?
                                     [connection-method/source-selector "HEADER_X_API_KEY"])}]])]

            [configuration-inputs/http-headers-section]

            [:> Flex {:align "center" :gap "3"}
             [:> Switch {:checked insecure-value
                         :size "3"
                         :onCheckedChange #(rf/dispatch [:connection-setup/update-claude-code-insecure %])}]
             [:> Box
              [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"}
               "Allow insecure SSL"]
              [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
               "Skip SSL certificate verification for HTTPS connections."]]]

            [agent-selector/main]]])))))
