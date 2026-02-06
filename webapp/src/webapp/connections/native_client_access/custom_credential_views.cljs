(ns webapp.connections.native-client-access.custom-credential-views
  (:require
   ["@radix-ui/themes" :refer [Box Heading Text]]
   [webapp.components.callout-link :as callout-link]
   [webapp.components.logs-container :as logs]))

(defn block-with-heading-and-text
  [{:keys [heading text log-id log-content]}]
  [:> Box {:class "space-y-2"}
   [:> Box
    [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
     heading]
    [:> Text {:size "2" :weight "regular" :class "text-[--gray-11]"}
     text]]
   [logs/new-container
    {:status :success
     :id log-id
     :logs log-content}]])

(defn claude-code-credentials-fields
  "Claude Code specific credentials fields"
  [{:keys [connection_credentials]}]
  (println "connection_credentials" connection_credentials)
  (let [hostname (:hostname connection_credentials)
        port (:port connection_credentials)
        proxy-token (:proxy_token connection_credentials)
        base-url (str "http://" hostname ":" port)
        custom-headers (str "Authorization: " proxy-token)
        build-json-content (fn [base-url custom-headers]
                             {:env {:ANTHROPIC_BASE_URL base-url
                                    :ANTHROPIC_CUSTOM_HEADERS custom-headers}})]
    [:<>

     ;; Anthropic API URL
     [block-with-heading-and-text
      {:heading "Create or modify settings.json"
       :text "Locate this file or create and access it via your preferred IDE"
       :log-id "anthropic-settings-json"
       :log-content "~/.claude/settings.json"}]


     ;; Anthropic API Key
     [block-with-heading-and-text
      {:heading "If the file or folder doesn’t exist"
       :text "Make sure the folder exists and create it:"
       :log-id "create-anthropic-settings-folder"
       :log-content "mkdir -p ~/.claude && touch ~/.claude/settings.json"}]


     [:> Box {:class "space-y-2"}
      [:> Box
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Add the following configuration"]
       [:> Text {:size "2" :weight "regular" :class "text-[--gray-11]"}
        "Modify the following values accordingly. If you have more settings, you can leave then, you only need to modify "
        [:> Text {:as "span" :size "2" :weight "bold" :class "text-[--gray-11]"}
         "ANTHROPIC_BASE_URL"]
        " and "
        [:> Text {:as "span" :size "2" :weight "bold" :class "text-[--gray-11]"}
         "ANTHROPIC_CUSTOM_HEADERS"]
        "."]]
      [logs/new-container
       {:status :success
        :id "anthropic-authorization-header"
        :logs [:pre (js/JSON.stringify (clj->js (build-json-content
                                                 base-url
                                                 custom-headers)) nil 2)]}]]

     [:> Box {:class "space-y-2"}
      [:> Box
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "In your favorite IDE"]
       [:> Text {:size "2" :weight "regular" :class "text-[--gray-11]"}
        "Open your IDE and run the Claude Code plugin."]]
      [callout-link/main {:href "https://www.claude.com/docs/cli/installation"
                          :text "See supported IDEs at Claude Code documentation."}]]

     [block-with-heading-and-text
      {:heading " In the Terminal "
       :text " Run Claude Code Command Line Interface "
       :log-id " claude-code-command-line-interface "
       :log-content " $ claude "}]]))
