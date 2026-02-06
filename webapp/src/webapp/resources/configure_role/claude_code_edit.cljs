(ns webapp.resources.configure-role.claude-code-edit
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Switch Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.configuration-inputs :as configuration-inputs]
   [webapp.connections.views.setup.connection-method :as connection-method]))

(defn claude-code-edit-form []
  (let [credentials (rf/subscribe [:connection-setup/claude-code-credentials])
        connection-method (rf/subscribe [:connection-setup/connection-method])
        env-vars (rf/subscribe [:connection-setup/environment-variables])]
    (fn []
      (r/with-let
        [initialized? (atom false)]

        (let [show-selector? (= @connection-method "secrets-manager")
              all-env-vars @env-vars
              x-api-key-env (some #(when (= (:key %) "X_API_KEY") %) all-env-vars)
              remote-url-value (if (map? (:remote_url @credentials))
                                 (:value (:remote_url @credentials))
                                 (or (:remote_url @credentials) "https://api.anthropic.com"))
              api-key-value (if (map? (:HEADER_X_API_KEY @credentials))
                              (:value (:HEADER_X_API_KEY @credentials))
                              (or (:HEADER_X_API_KEY @credentials)
                                  (when x-api-key-env
                                    (if (map? (:value x-api-key-env))
                                      (:value (:value x-api-key-env))
                                      (:value x-api-key-env)))
                                  ""))
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
                                  [connection-method/source-selector "HEADER_X_API_KEY"])}]]

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
