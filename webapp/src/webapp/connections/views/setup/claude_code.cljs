(ns webapp.connections.views.setup.claude-code
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text Heading Switch]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.configuration-inputs :as configuration-inputs]
   [webapp.connections.views.setup.connection-method :as connection-method]))

(defn claude-code-credentials-form []
  (let [credentials @(rf/subscribe [:connection-setup/network-credentials])
        connection-method @(rf/subscribe [:connection-setup/connection-method])
        show-selector? (= connection-method "secrets-manager")
        remote-url-value (if (map? (:remote_url credentials))
                           (:value (:remote_url credentials))
                           (or (:remote_url credentials) ""))
        api-key-value (if (map? (:HEADER_X_API_KEY credentials))
                        (:value (:HEADER_X_API_KEY credentials))
                        (or (:HEADER_X_API_KEY credentials) ""))
        insecure-value (let [raw-insecure (:insecure credentials)]
                         (cond
                           (boolean? raw-insecure) raw-insecure
                           (map? raw-insecure) (let [value-str (:value raw-insecure)]
                                                 (if (string? value-str)
                                                   (= value-str "true")
                                                   (boolean value-str)))
                           (string? raw-insecure) (= raw-insecure "true")
                           :else false))]
    [:form
     {:id "credentials-form"
      :on-submit (fn [e]
                   (.preventDefault e)
                   (rf/dispatch [:connection-setup/next-step :additional-config]))}
     [:> Box {:class "max-w-[600px]"}
      [:> Box {:class "space-y-7"}
       [connection-method/main "claude-code"]

       [:> Box {:class "space-y-4"}
        [:> Text {:size "4" :weight "bold"} "Environment credentials"]

        ;; Anthropic API URL input
        [forms/input
         {:label "Anthropic API URL"
          :placeholder "https://api.anthropic.com"
          :required true
          :value (if (empty? remote-url-value) "https://api.anthropic.com" remote-url-value)
          :on-change #(rf/dispatch [:connection-setup/update-network-credentials
                                    "remote_url"
                                    (-> % .-target .-value)])
          :start-adornment (when show-selector?
                             [connection-method/source-selector "remote_url"])}]

        ;; Anthropic API Key input
        [forms/input
         {:label "Anthropic API Key"
          :placeholder "sk-ant-..."
          :required true
          :type "password"
          :value api-key-value
          :on-change #(rf/dispatch [:connection-setup/update-network-credentials
                                    "HEADER_X_API_KEY"
                                    (-> % .-target .-value)])
          :start-adornment (when show-selector?
                             [connection-method/source-selector "HEADER_X_API_KEY"])}]]

       ;; HTTP Headers Section
       [configuration-inputs/http-headers-section]

       ;; Allow insecure SSL switch
       [:> Flex {:align "center" :gap "3"}
        [:> Switch {:checked insecure-value
                    :size "3"
                    :onCheckedChange #(rf/dispatch [:connection-setup/toggle-network-insecure (boolean %)])}]
        [:> Box
         [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"}
          "Allow insecure SSL"]
         [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
          "Skip SSL certificate verification for HTTPS connections."]]]

       [agent-selector/main]]]]))
