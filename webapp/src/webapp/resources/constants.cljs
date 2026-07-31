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
