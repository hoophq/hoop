(ns webapp.resources.setup.events.mcp-catalog
  "Server picker for the MCP Gateway (mcpproxy) connection type.

  The gateway serves mcpproxy's built-in catalog of publicly hosted remote MCP
  servers at GET /mcp-catalog. Picking an entry pre-fills the endpoint,
  transport and auth mode so an admin does not have to look those values up;
  the \"custom\" choice leaves every field editable for a server the catalog
  does not know, including local stdio servers.

  The catalog is static build-time data on the gateway side, so it is fetched
  once per app session and cached in app-db under [:mcp-catalog]:

    {:status  :idle | :loading | :loaded | :error
     :entries [{:name :description :url :transport :auth :header :notes}]}"
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]))

;; ---------------------------------------------------------------------------
;; Fetch + cache
;; ---------------------------------------------------------------------------

(rf/reg-event-fx
 :mcp-catalog/fetch
 (fn [{:keys [db]} _]
   ;; Already loaded or in flight: the catalog cannot change within a session.
   (if (contains? #{:loading :loaded} (get-in db [:mcp-catalog :status]))
     {}
     {:db (assoc-in db [:mcp-catalog :status] :loading)
      :fx [[:dispatch [:fetch {:method "GET"
                               :uri "/mcp-catalog"
                               :on-success #(rf/dispatch [:mcp-catalog/fetch-success %])
                               :on-failure #(rf/dispatch [:mcp-catalog/fetch-failure %])}]]]})))

(rf/reg-event-db
 :mcp-catalog/fetch-success
 (fn [db [_ response]]
   (assoc db :mcp-catalog {:status :loaded
                           :entries (js->clj response :keywordize-keys true)})))

(rf/reg-event-db
 :mcp-catalog/fetch-failure
 (fn [db [_ _error]]
   ;; A failed catalog fetch must not block the form: the admin can still fill
   ;; the fields by hand, which is exactly the "custom" path.
   (assoc db :mcp-catalog {:status :error :entries []})))

(rf/reg-sub
 :mcp-catalog/state
 (fn [db _]
   (get db :mcp-catalog {:status :idle :entries []})))

;; ---------------------------------------------------------------------------
;; Applying a selection
;; ---------------------------------------------------------------------------

(defn- entry-by-name
  [entries server-name]
  (first (filter #(= (:name %) server-name) entries)))

(defn static-header-name
  "Header name a static-auth entry expects, from its \"Name: ${TEMPLATE}\"
  header field. Returns nil for entries that do not use a static credential.

  The name matters exactly: context7 wants CONTEXT7_API_KEY and google-maps
  wants X-Goog-Api-Key. Sending the token under the wrong header name is an
  unauthenticated request, not a warning."
  [entry]
  (when (= (:auth entry) "static")
    (let [header (or (:header entry) "")
          [name-part] (str/split header #":" 2)
          n (str/trim (or name-part ""))]
      (when-not (str/blank? n) n))))

(rf/reg-event-fx
 :mcp-catalog/select-server
 (fn [{:keys [db]} [_ role-index server-name]]
   (let [entries (get-in db [:mcp-catalog :entries] [])
         entry (entry-by-name entries server-name)
         set-cred (fn [k v] [:dispatch [:resource-setup->update-role-credentials role-index k v]])]
     (if (nil? entry)
       ;; "custom": remember the choice and leave the fields as the admin left
       ;; them. Clearing them here would discard a half-typed URL.
       {:fx [(set-cred "mcp_server" "custom")]}
       {:fx (cond-> [(set-cred "mcp_server" server-name)
                     (set-cred "remote_url" (:url entry))
                     (set-cred "mcp_transport" (:transport entry))
                     ;; A catalog entry's auth mode is advisory for the agent,
                     ;; which only accepts none|static. An oauth server still
                     ;; authenticates through hoop's own /mcp-oauth broker,
                     ;; whose result is frozen into HEADER_AUTHORIZATION — a
                     ;; static credential.
                     (set-cred "mcp_auth" (if (= (:auth entry) "none") "none" "static"))]
              ;; Record which header the provider expects so the form can ask
              ;; for the token by name instead of making the admin decode a
              ;; "${CONTEXT7_API_KEY}" template.
              true (conj (set-cred "mcp_static_header" (or (static-header-name entry) ""))))}))))
