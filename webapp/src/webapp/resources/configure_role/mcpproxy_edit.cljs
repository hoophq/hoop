(ns webapp.resources.configure-role.mcpproxy-edit
  "MCP Gateway (mcpproxy) credentials on the connection EDIT screen.

  This renders the CREATE form — webapp.resources.setup.roles-step's
  mcpproxy-role-form — against a role hydrated from the saved connection,
  rather than carrying a second copy of it. The hydration itself belongs to
  the edit screen's lifecycle (configure-role.main, which already owns
  loading the connection and tearing the form state down), so this namespace
  is a plain function of app-db.

  Every other httpproxy subtype has its own edit form, so the shape here is
  deliberate. What makes this subtype different is that its env vars are
  settings the agent parses (MCP_TRANSPORT, MCP_AUTH, the tool policy) rather
  than outbound HTTP headers, and that they constrain each other: a stdio
  transport takes a command and MCPENV_* and refuses MCP_AUTH=passthrough, a
  remote transport takes a URL and HEADER_*. A second form is a second place
  for those rules to be spelled out, and the create form already grew three
  bugs' worth of them.

  The edit-only piece is the OAuth widget's flow id. Re-authorizing here has
  to produce one, because a connection whose token was frozen at create time
  stops working when the provider expires it, and only the flow id lets the
  gateway adopt that login into a renewable grant (adoptMCPOAuthGrant). The
  create-flow OAuth events write it to the role, and process-payload reads it
  from there."
  (:require
   ["@radix-ui/themes" :refer [Box]]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.resources.setup.connection-method :as connection-method]
   [webapp.resources.setup.roles-step :as roles-step]))

;; The create form is role-indexed; the edit screen edits exactly one
;; connection, so it always occupies role 0.
(def edit-role-index 0)

(defn mcpproxy-edit-form []
  [:form
   {:id "credentials-form"
    :on-submit (fn [e] (.preventDefault e))}
   [:> Box {:class "space-y-radix-6"}
    [connection-method/main edit-role-index]
    [roles-step/mcpproxy-role-form edit-role-index]
    [agent-selector/main]]])
