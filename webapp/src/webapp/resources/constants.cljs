(ns webapp.resources.constants)

;; Role configuration fields for different types
(def role-configs-required
  {:application/ssh [{:key "host" :label "Host" :value "" :required true}
                     {:key "port" :label "Port" :value "" :required false}
                     {:key "user" :label "User" :value "" :required true}
                     {:key "pass" :label "Pass" :value "" :required false}
                     {:key "authorized_server_keys"
                      :label "Private Key"
                      :value ""
                      :required false
                      :placeholder "Enter your private key"
                      :type "textarea"}]
   :application/tcp [{:key "host" :label "Host" :value "" :required true}
                     {:key "port" :label "Port" :value "" :required true}]
   :application/httpproxy [{:key "remote_url" :label "Remote URL" :value "" :required true}
                           {:key "insecure" :label "Insecure" :value "false" :required false :type "checkbox"}]})

;; Get role config based on type and subtype
(defn get-role-config [type subtype]
  (get role-configs-required (keyword (str type "/" subtype))))

(def http-proxy-subtypes
  "Set of connection subtypes that use HTTP proxy logic.

  mcpproxy is the protocol-aware MCP type (ADR-0004). It is not a byte relay,
  but it shares every trait this set gates: HEADER_ prefixing for credentials,
  no web terminal, native-client access, and a credential-source selector."
  #{"httpproxy" "kibana" "grafana" "claude-code" "mcp" "mcpproxy"})

(def mcp-stdio-transports
  "MCP_TRANSPORT values that spawn a child process instead of reaching a URL.

  Which transport is selected decides what the rest of an mcpproxy role means:
  a command instead of a REMOTE_URL, MCPENV_* instead of HEADER_*, and no
  MCP_AUTH at all. Three places have to agree on that — the form that renders
  the fields, the events that clear the ones the other transport left behind,
  and the payload builder that carves the command out of the env vars — so the
  set lives here rather than being spelled out in each of them.

  The two differ only in WHICH machine runs the command: \"stdio\" is the agent
  host, \"client-stdio\" is the laptop of whoever runs `hoop connect`."
  #{"stdio" "client-stdio"})
