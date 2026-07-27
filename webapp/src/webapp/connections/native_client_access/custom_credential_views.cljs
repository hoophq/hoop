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
  [{:keys [connection_credentials connection_name]}]
  (let [hostname (:hostname connection_credentials)
        port (:port connection_credentials)
        proxy-token (:proxy_token connection_credentials)
        vertex-project (:vertex_project_id connection_credentials)
        vertex-region (:vertex_region connection_credentials)
        vertex? (boolean (seq vertex-project))
        protocol (-> js/window .-location .-protocol)
        base-url (str protocol "//" hostname ":" port)
        custom-headers (str "Authorization: " proxy-token)
        ;; Vertex mode forwards the full Vertex path through hoop, so the base
        ;; URL must keep the `/v1` segment the Anthropic Vertex SDK appends
        ;; `/projects/...` to. The gateway proxy preserves the request path
        ;; verbatim, so `/v1` has to come from the client base URL.
        json-content (if vertex?
                       {:env {:CLAUDE_CODE_USE_VERTEX "1"
                              :CLAUDE_CODE_SKIP_VERTEX_AUTH "1"
                              :ANTHROPIC_VERTEX_PROJECT_ID vertex-project
                              :CLOUD_ML_REGION vertex-region
                              :ANTHROPIC_VERTEX_BASE_URL (str base-url "/v1")
                              :ANTHROPIC_AUTH_TOKEN proxy-token}}
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
        "Modify the following values accordingly. If you have more settings, you can leave them, you only need to modify "
        [:> Text {:as "span" :size "2" :weight "bold" :class "text-[--gray-11]"}
         (if vertex? "ANTHROPIC_VERTEX_BASE_URL" "ANTHROPIC_BASE_URL")]
        " and "
        [:> Text {:as "span" :size "2" :weight "bold" :class "text-[--gray-11]"}
         (if vertex? "ANTHROPIC_AUTH_TOKEN" "ANTHROPIC_CUSTOM_HEADERS")]
        "."]]
      [logs/new-container
       {:status :success
        :id "anthropic-authorization-header"
        :logs [:pre (js/JSON.stringify (clj->js json-content) nil 2)]}]
      [:> Box {:class "pt-1"}
       [:> Text {:size "2" :weight "regular" :class "text-[--gray-11]"}
        "Or run this command to apply automatically:"]
       [:> Box {:class "mt-2"}
        [logs/new-container
         {:status :success
          :id "claude-code-cli-configure"
          :logs (str "hoop claude configure " connection_name)}]]]]

     [:> Box {:class "space-y-2"}
      [:> Box
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "In your favorite IDE"]
       [:> Text {:size "2" :weight "regular" :class "text-[--gray-11]"}
        "Open your IDE and run the Claude Code plugin."]]
      [callout-link/main {:href "https://code.claude.com/docs/en/overview"
                          :text "See supported IDEs at Claude Code documentation."}]]

     [block-with-heading-and-text
      {:heading " In the Terminal "
       :text " Run Claude Code Command Line Interface "
       :log-id " claude-code-command-line-interface "
       :log-content " $ claude "}]]))
