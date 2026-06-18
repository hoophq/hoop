(ns webapp.resources.configure-role.mcp-edit
  "MCP httpproxy subtype edit form. Mirrors the create-flow mcp-role-form
  (webapp.resources.setup.roles-step) but binds to the edit flow's
  [:connection-setup] state and the edit-flow OAuth events in
  webapp.resources.configure-role.mcp-oauth-edit."
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Switch Text]]
   ["lucide-react" :refer [Check ShieldCheck]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.configuration-inputs :as configuration-inputs]
   [webapp.connections.views.setup.connection-method :as connection-method]))

(defn- authorized? [env-vars]
  (boolean
   (some (fn [{:keys [key value]}]
           (and (= "authorization" (cs/lower-case (or key "")))
                (not (cs/blank? (str value)))))
         env-vars)))

(defn mcp-edit-form []
  (let [credentials (rf/subscribe [:connection-setup/network-credentials])
        connection-method (rf/subscribe [:connection-setup/connection-method])
        env-vars (rf/subscribe [:connection-setup/environment-variables])
        mcp-state (rf/subscribe [:mcp-oauth/edit-state])]
    (fn []
      (let [show-selector? (= @connection-method "secrets-manager")
            remote-url-value (if (map? (:remote_url @credentials))
                               (:value (:remote_url @credentials))
                               (or (:remote_url @credentials) ""))
            insecure-value (let [raw-insecure (:insecure @credentials)]
                             (cond
                               (boolean? raw-insecure) raw-insecure
                               (map? raw-insecure) (boolean (:value raw-insecure))
                               (string? raw-insecure) (= raw-insecure "true")
                               :else false))
            authed? (authorized? @env-vars)
            status (:status @mcp-state :idle)
            busy? (contains? #{:authorizing :pending} status)]
        [:form
         {:id "credentials-form"
          :on-submit (fn [e] (.preventDefault e))}
         [:> Box {:class "space-y-radix-6"}
          [connection-method/main "mcp"]

          [:> Box {:class "space-y-radix-4"}
           [:> Heading {:size "4" :weight "bold"} "Basic info"]

           [forms/input
            {:label "MCP Server URL"
             :placeholder "e.g. https://mcp.figma.com/mcp"
             :required true
             :value remote-url-value
             :on-change #(rf/dispatch [:connection-setup/update-network-credentials
                                       "remote_url"
                                       (-> % .-target .-value)])
             :start-adornment (when show-selector?
                                [connection-method/source-selector "remote_url"])}]]

          ;; Authorization section (mirrors the create-flow mcp-role-form)
          [:> Box {:class "space-y-4 rounded-md border border-[--gray-5] p-4"}
           [:> Box
            [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"}
             "MCP Authorization"]
            [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
             "Log in to the MCP server to obtain an access token. The token is stored in this connection's Authorization header."]]

           ;; Optional pre-registered client credentials. Left blank, Hoop
           ;; registers a client dynamically. Auth-flow only \u2014 never stored as
           ;; connection environment variables.
           [:> Box {:class "space-y-3"}
            [forms/input {:label "Client ID (optional)"
                          :placeholder "Leave blank to register automatically"
                          :value (or (:client-id @mcp-state) "")
                          :type "text"
                          :on-change #(rf/dispatch [:mcp-oauth/edit-set-field :client-id (-> % .-target .-value)])}]
            [forms/input {:label "Client Secret (optional)"
                          :placeholder "Only if your client requires one"
                          :value (or (:client-secret @mcp-state) "")
                          :type "password"
                          :on-change #(rf/dispatch [:mcp-oauth/edit-set-field :client-secret (-> % .-target .-value)])}]]

           (cond
             authed?
             [:> Flex {:align "center" :justify "between" :gap "3"}
              [:> Flex {:align "center" :gap "2"}
               [:> Check {:size 16 :class "text-[--grass-11]"}]
               [:> Text {:size "2" :weight "medium" :class "text-[--grass-11]"}
                "Authorized \u2014 access token stored"]]
              [:> Flex {:gap "2"}
               [:> Button {:size "2" :type "button" :variant "soft" :disabled busy?
                           :on-click #(rf/dispatch [:mcp-oauth/edit-authorize])}
                "Re-authorize"]
               [:> Button {:size "2" :type "button" :variant "ghost" :color "red" :class "pt-3"
                           :on-click #(rf/dispatch [:mcp-oauth/edit-clear])}
                "Clear"]]]

             :else
             [:> Box {:class "space-y-2"}
              [:> Button {:size "2" :type "button" :variant "solid" :disabled busy?
                          :on-click #(rf/dispatch [:mcp-oauth/edit-authorize])}
               [:> ShieldCheck {:size 16}]
               (if busy? "Authorizing\u2026" "Authorize with MCP")]
              (when (= status :error)
                [:> Text {:as "p" :size "2" :class "text-[--red-11]"}
                 (or (:error @mcp-state) "Authorization failed")])])]

          [configuration-inputs/http-headers-section]

          [:> Flex {:align "center" :gap "3"}
           [:> Switch {:checked insecure-value
                       :size "3"
                       :onCheckedChange #(rf/dispatch [:connection-setup/toggle-network-insecure (boolean %)])}]
           [:> Box
            [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"}
             "Allow insecure SSL"]
            [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
             "Skip SSL certificate verification for HTTPS connections."]]]

          [agent-selector/main]]]))))
