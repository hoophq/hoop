(ns webapp.connections.native-client-access.custom-credential-views
  (:require
   ["@radix-ui/themes" :refer [Box Heading Text]]
   [clojure.string :as cs]
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

;; ---------------------------------------------------------------------------
;; MCP Gateway (mcpproxy)
;; ---------------------------------------------------------------------------
;;
;; An MCP client does not take a host/port/username/password. It takes one
;; endpoint URL plus an Authorization header, written into that client's own
;; JSON config file. So this view hands the user the exact snippet to paste,
;; per tool, rather than a set of fields they would have to assemble.
;;
;; Formats verified against each vendor's documentation:
;;   Claude Code  https://code.claude.com/docs/en/mcp
;;   Cursor       https://cursor.com/docs/mcp
;;   Devin CLI    https://docs.devin.ai/cli/extensibility/mcp/configuration
;;
;; All three accept the same shape for a remote server — url + headers — under
;; a "mcpServers" object. They disagree only on the transport discriminator:
;; Claude Code requires an explicit "type" (an entry with a url and no type is
;; a documented configuration error), Devin uses "transport", and Cursor infers
;; it from the presence of "url".

(defn- mcp-servers-json
  "Render a client config snippet. `extra` carries the client-specific
  transport discriminator."
  [server-name url token extra]
  (js/JSON.stringify
   (clj->js {:mcpServers
             {server-name (merge extra
                                 {:url url
                                  :headers {:Authorization token}})}})
   nil 2))

(defn mcp-proxy-credentials-fields
  "MCP Gateway setup instructions: the endpoint plus a ready-to-paste config
  block for each supported MCP client."
  [{:keys [connection_credentials connection_name]}]
  (let [{:keys [proxy_token port]} connection_credentials
        hostname (let [h (.-hostname js/location)]
                   (if (= h "localhost") "127.0.0.1" h))
        protocol (.-protocol js/location)
        url (str protocol "//" hostname ":" port "/mcp")
        ;; The server key must be a valid identifier in every client's config;
        ;; connection names allow characters these files do not. Runs of
        ;; substituted characters collapse to one dash so "test role 8813!"
        ;; reads as "test-role-8813" rather than "test-role-8813-".
        server-name (-> (or connection_name "hoop-mcp")
                        (cs/replace #"[^A-Za-z0-9_-]+" "-")
                        (cs/replace #"^-+|-+$" ""))]
    [:<>
     [block-with-heading-and-text
      {:heading "MCP endpoint"
       :text "Every client below points at this URL. Traffic is inspected by hoop before it reaches the MCP server."
       :log-id "mcp-endpoint-url"
       :log-content url}]

     [block-with-heading-and-text
      {:heading "Authorization header"
       :text "Your personal access token for this connection. Treat it as a credential — it is already embedded in the snippets below."
       :log-id "mcp-proxy-token"
       :log-content proxy_token}]

     ;; ---- Claude Code ---------------------------------------------------
     [:> Box {:class "space-y-2"}
      [:> Box
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Claude Code"]
       [:> Text {:size "2" :weight "regular" :class "text-[--gray-11]"}
        "Run this command, or add the block below to .mcp.json in your project."]]
      [logs/new-container
       {:status :success
        :id "mcp-claude-code-cli"
        :logs (str "claude mcp add --transport http " server-name " " url
                   " --header \"Authorization: " proxy_token "\"")}]
      [:> Box {:class "mt-2"}
       [logs/new-container
        {:status :success
         :id "mcp-claude-code-json"
         ;; Claude Code treats a url entry with no "type" as a stdio server
         ;; and skips it, so the discriminator is mandatory here.
         :logs [:pre (mcp-servers-json server-name url proxy_token {:type "http"})]}]]]

     ;; ---- Cursor ----------------------------------------------------------
     [:> Box {:class "space-y-2"}
      [:> Box
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Cursor"]
       [:> Text {:size "2" :weight "regular" :class "text-[--gray-11]"}
        "Add to ~/.cursor/mcp.json for every project, or .cursor/mcp.json for this one."]]
      [logs/new-container
       {:status :success
        :id "mcp-cursor-json"
        ;; Cursor infers the remote transport from the presence of "url".
        :logs [:pre (mcp-servers-json server-name url proxy_token {})]}]]

     ;; ---- Devin -----------------------------------------------------------
     [:> Box {:class "space-y-2"}
      [:> Box
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Devin"]
       [:> Text {:size "2" :weight "regular" :class "text-[--gray-11]"}
        "Run this command, or add the block below to .devin/mcp_config.json."]]
      [logs/new-container
       {:status :success
        :id "mcp-devin-cli"
        :logs (str "devin mcp add " server-name " " url)}]
      [:> Box {:class "mt-2"}
       [logs/new-container
        {:status :success
         :id "mcp-devin-json"
         :logs [:pre (mcp-servers-json server-name url proxy_token {:transport "http"})]}]]]

     ;; ---- Anything else ---------------------------------------------------
     [:> Box {:class "space-y-2"}
      [:> Box
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Other MCP clients"]
       [:> Text {:size "2" :weight "regular" :class "text-[--gray-11]"}
        "Any client that speaks streamable HTTP works. Point it at the endpoint above and send the Authorization header with every request."]]
      [callout-link/main {:href "https://modelcontextprotocol.io/docs/develop/connect-local-servers"
                          :text "See the Model Context Protocol documentation."}]]]))
