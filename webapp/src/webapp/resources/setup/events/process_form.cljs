(ns webapp.resources.setup.events.process-form
  (:require
   [clojure.string :as str]
   [webapp.resources.helpers :as helpers]
   [webapp.resources.constants :refer [http-proxy-subtypes mcp-stdio-transports]]))

(defn extract-value
  "Extract value from map or string, applying prefix based on the chosen source."
  [v connection-method field-key secrets-provider]
  (let [{:keys [value source]}
        (if (map? v)
          {:value (:value v "")
           :source (:source v)}
          {:value (if (boolean? v)
                    (str v)
                    (str v))
           :source nil})

        default-source (if (= connection-method "secrets-manager")
                         secrets-provider
                         "manual-input")
        source (or source default-source)
        prefix (helpers/get-secret-prefix source)

        field (-> field-key name str/lower-case)
        aws-iam? (= connection-method "aws-iam-role")
        user-or-pass? (#{"user" "pass"} field)

        ;; AWS IAM Role pass should always be "authtoken"
        final-value (if (and aws-iam? (= field "pass") (str/blank? value))
                      "authtoken"
                      value)]
    (cond
      ;; AWS IAM Role: apply _aws_iam_rds: prefix to user/pass
      (and aws-iam? user-or-pass?)
      (str "_aws_iam_rds:" final-value)

      ;; For non-AWS IAM Role, apply prefix if present
      (and (not aws-iam?) (not (str/blank? prefix)))
      (str prefix final-value)

      :else
      final-value)))

(defn raw-credential-value
  "Returns the plain string held by a role credential, unwrapping the
  {:value :source} secrets-manager shape.

  Public because every read of a role credential has to go through it, not
  just the ones in this namespace. A credential is a raw string until the
  admin touches the connection-method radio, at which point
  update-role-credentials-source rewraps EVERY credential — in both
  directions, since switching back to manual-input rewraps too. Code that
  compared a credential against a string therefore worked right up until the
  admin toggled that radio, and then quietly stopped: for mcp_transport that
  meant the stdio carve-outs below all reverted to the remote behaviour."
  [v]
  (cond
    (map? v) (str (:value v ""))
    (nil? v) ""
    :else (str v)))

(def mcp-ui-only-credentials
  "MCP Gateway form state that must never become a connection env var: the
  catalog entry that pre-filled the form, the auth mode the admin selected,
  and the header name a pasted token rides in. All three are already
  represented in what the agent does read — MCP_TRANSPORT, MCP_AUTH, and the
  HEADER_<name> key itself."
  ["mcp_server" "mcp_auth_mode" "mcp_static_header"])

(defn claude-code-vertex-remote-url
  "Builds the Google Vertex AI host the agent proxies claude-code traffic to,
  from the configured GCP region. A blank or \"global\" region resolves to the
  global endpoint; any other region uses the regional endpoint."
  [region]
  (let [r (str/trim (or region ""))]
    (if (or (str/blank? r) (= r "global"))
      "https://aiplatform.googleapis.com"
      (str "https://" r "-aiplatform.googleapis.com"))))

(defn claude-code-secret-credentials
  "Builds the credential map emitted for a claude-code role. The UI-only
  \"provider\" marker and the inactive provider's fields are dropped so they
  never leak into the connection secret. For the Google Vertex AI provider the
  regional Vertex host is derived into REMOTE_URL (forced to manual-input so it
  is never treated as a secret reference)."
  [credentials]
  (let [provider (raw-credential-value (get credentials "provider"))
        insecure (get credentials "insecure" false)]
    (if (= provider "vertex")
      ;; The Vertex fields are entered as literal values (no source-selector),
      ;; so they are forced to manual-input — otherwise a role configured for a
      ;; secrets manager would prefix them as secret references.
      {"REMOTE_URL" {:value (claude-code-vertex-remote-url
                             (raw-credential-value (get credentials "GCP_REGION")))
                     :source "manual-input"}
       "GCP_SERVICE_ACCOUNT_JSON" {:value (raw-credential-value (get credentials "GCP_SERVICE_ACCOUNT_JSON"))
                                   :source "manual-input"}
       "GCP_PROJECT_ID" {:value (raw-credential-value (get credentials "GCP_PROJECT_ID"))
                         :source "manual-input"}
       "GCP_REGION" {:value (raw-credential-value (get credentials "GCP_REGION"))
                     :source "manual-input"}
       "insecure" insecure}
      {"remote_url" (get credentials "remote_url")
       "HEADER_X_API_KEY" (get credentials "HEADER_X_API_KEY")
       "insecure" insecure})))

(defn mcpproxy-stdio?
  "True when this role's credentials configure an MCP server hoop spawns as a
  child process rather than reaches at a URL.

  Both stdio transports answer yes: they differ only in WHICH machine runs the
  command, never in how it is carried. Every stdio carve-out downstream keys
  off this — the command leaves the env vars for the connection's command
  array, secrets take the MCPENV_ prefix instead of HEADER_, and remote-only
  settings are dropped — so all of them agree by construction."
  [subtype credentials]
  (and (= subtype "mcpproxy")
       (contains? mcp-stdio-transports
                  (raw-credential-value (get credentials "mcp_transport")))))

(defn mcpproxy-stdio-command
  "The connection command array for a stdio MCP role, or nil when the role is
  not one.

  A stdio server is spawned from the connection's command array
  (AgentConnectionParams.CmdList), so the command the admin typed belongs
  there rather than in an env var. Split on whitespace: the field takes a
  plain command line."
  [subtype credentials]
  (when (mcpproxy-stdio? subtype credentials)
    (->> (str/split (raw-credential-value (get credentials "command")) #"\s+")
         (remove str/blank?)
         vec)))

(defn process-role-secret
  "Process role credentials into secret format with base64 encoding"
  [role]
  (let [subtype (:subtype role)
        connection-method (:connection-method role)
        secrets-provider (or (:secrets-manager-provider role) "vault-kv1")
        raw-credentials (if (= subtype "claude-code")
                          (claude-code-secret-credentials (:credentials role))
                          (:credentials role))
        ;; auth-method and connection-type are UI-only role state, never secrets.
        ;; Local SSH runs on the agent host itself, so it carries no credentials.
        ssh-local? (and (= subtype "ssh")
                        (= (raw-credential-value
                            (get (:credentials role) "connection-type"))
                           "local"))
        ;; A stdio MCP server's command becomes the connection's command array
        ;; (see process-role), so it must not also be emitted as an env var.
        stdio? (mcpproxy-stdio? subtype (:credentials role))
        credentials (cond
                      ssh-local? {}
                      :else
                      ;; The MCP form keeps three keys the agent never reads:
                      ;; which catalog entry pre-filled the form, which auth
                      ;; mode the admin chose, and which header a pasted token
                      ;; rides in (already encoded in that token's own key).
                      ;; Emitting them would create MCP_SERVER / MCP_AUTH_MODE /
                      ;; MCP_STATIC_HEADER env vars nothing consumes. The mode
                      ;; still reaches the agent, as MCP_AUTH.
                      (cond-> (apply dissoc raw-credentials
                                     "auth-method" "connection-type" mcp-ui-only-credentials)
                        stdio? (dissoc "command" "remote_url" "insecure")))
        metadata-credentials (:metadata-credentials role)
        env-vars (or (:environment-variables role) [])
        config-files (or (:configuration-files role) [])
        is-aws-iam-role? (= connection-method "aws-iam-role")
        ;; For AWS IAM Role, always ensure PASS field is set to "authtoken"
        metadata-credentials-with-pass (if is-aws-iam-role?
                                         (let [pass-key (or (first (filter #(= (str/lower-case (name %)) "pass") (keys metadata-credentials)))
                                                            "PASS")]
                                           (assoc (or metadata-credentials {}) pass-key {:value "authtoken" :source "aws-iam-role"}))
                                         metadata-credentials)

        credential-env-vars (mapv (fn [[k v]]
                                    {:key (name k)
                                     :value (if (boolean? v)
                                              (str v)
                                              (extract-value v connection-method k secrets-provider))})
                                  (seq credentials))

        metadata-credential-env-vars (mapv (fn [[k v]]
                                             {:key (name k)
                                              :value (extract-value v connection-method k secrets-provider)})
                                           (seq metadata-credentials-with-pass))

        ;; Combine all credentials
        all-credential-env-vars (concat credential-env-vars metadata-credential-env-vars)

        ;; Process environment variables with prefixes
        processed-env-vars (mapv (fn [{:keys [key value]}]
                                   {:key key
                                    :value (extract-value value connection-method (keyword key) secrets-provider)})
                                 env-vars)

        ;; Special handling for httpproxy headers
        all-env-vars (cond
                       ;; A stdio MCP server needs its secrets in the child
                       ;; process environment, which the agent collects from
                       ;; the MCPENV_ carve-out prefix. HEADER_ would make them
                       ;; outbound HTTP headers, which a subprocess never sees.
                       stdio?
                       (concat all-credential-env-vars
                               (mapv (fn [{:keys [key value]}]
                                       {:key (str "MCPENV_" key)
                                        :value (extract-value value connection-method (keyword key) secrets-provider)})
                                     (:environment-variables role [])))

                       (http-proxy-subtypes subtype)
                       (let [headers (:environment-variables role [])
                             processed-headers (mapv (fn [{:keys [key value]}]
                                                       {:key (str "HEADER_" key)
                                                        :value (extract-value value connection-method (keyword key) secrets-provider)})
                                                     headers)]
                         (concat all-credential-env-vars processed-headers))

                       :else
                       (concat all-credential-env-vars processed-env-vars))

        envvar-result (helpers/config->json all-env-vars "envvar:" subtype)
        filesystem-result (when (seq config-files)
                            (helpers/config->json config-files "filesystem:"))]

    (clj->js
     (merge envvar-result filesystem-result))))

(defn find-connection-metadata
  "Returns the resourceConfiguration metadata entry that matches the given
  subtype, or nil if no match. `resource-metadata` is the list under
  `[:connections :metadata :data :connections]` in app-db (loaded from
  `/data/connections-metadata.json`)."
  [resource-metadata subtype]
  (when (and resource-metadata subtype)
    (->> resource-metadata
         (filter #(= (get-in % [:resourceConfiguration :subtype]) subtype))
         first)))

(defn process-role
  "Process a single role into the format expected by the API"
  [role agent-id & [command-role]]
  (let [type (:type role)
        raw-subtype (:subtype role)
        ;; Local SSH is submitted with the wire subtype "ssh-local"; the role
        ;; keeps subtype "ssh" so process-role-secret drops its credentials.
        ssh-local? (and (= raw-subtype "ssh")
                        (= (raw-credential-value
                            (get (:credentials role) "connection-type"))
                           "local"))
        subtype (if ssh-local? "ssh-local" raw-subtype)
        secret (process-role-secret role)
        command-role (if command-role
                       command-role
                       (:command role))

        ;; Build command array for custom types
        ;; command-args is stored as array of {"value": "...", "label": "..."}
        ;; Extract just the values

        command-args (:command-args role [])
        stdio-command (mcpproxy-stdio-command raw-subtype (:credentials role))
        command (cond
                  (seq stdio-command) stdio-command
                  (and (= type "custom") (= subtype "linux-vm"))
                  (mapv #(get % "value") command-args)
                  :else (or command-role []))]

    (cond-> {:name (:name role)
             :type type
             :subtype subtype
             :agent_id agent-id
             :secret secret
             :command command
             :attributes (vec (or (:attributes role) []))
             :access_mode_runbooks "enabled"
             :access_mode_exec "enabled"
             :access_mode_connect "enabled"
             :access_schema "enabled"
             :redact_enabled false
             :redact_types []
             :reviewers []}
      ;; An MCP Gateway role that completed an OAuth login carries the flow id
      ;; so the gateway can adopt that login into a durable grant for the
      ;; connection it is about to create. Without it the connection keeps
      ;; only the frozen HEADER_AUTHORIZATION, which stops working when the
      ;; provider expires the token.
      (and (= raw-subtype "mcpproxy")
           (not (str/blank? (get-in role [:mcp-oauth :flow-id]))))
      (assoc :mcp_oauth_flow_id (get-in role [:mcp-oauth :flow-id])))))

(defn with-profile-attribute
  "While a protection profile is active, new roles carry its attribute so the
  profile rules apply immediately. The managed pill in the wizard is
  pre-selected; removing it (skip-protection-profile?) opts the role out and
  the attribute is simply not sent."
  [processed-role role db]
  (let [attr (get-in db [:protection-profile :active :attribute-name])]
    (if (and attr (not (:skip-protection-profile? role)))
      (update processed-role :attributes
              (fn [attrs] (vec (distinct (conj (or attrs []) attr)))))
      processed-role)))

(defn finalize-role-current-values
  "Add current (uncommitted) env vars and config files to a role before processing"
  [role]
  (let [;; Get current env var values
        env-current-key (:env-current-key role)
        env-current-value-map (:env-current-value role)
        env-current-value (if (map? env-current-value-map)
                            (:value env-current-value-map)
                            env-current-value-map)
        has-pending-env? (and (not (str/blank? env-current-key))
                              (not (str/blank? env-current-value)))

        ;; Get current config file values
        config-current-name (:config-current-name role)
        config-current-content (:config-current-content role)
        has-pending-config? (and (not (str/blank? config-current-name))
                                 (not (str/blank? config-current-content)))

        ;; Add pending env var if exists
        updated-env-vars (if has-pending-env?
                           (let [env-current-value-final (if (map? env-current-value-map)
                                                           env-current-value-map
                                                           {:value env-current-value :source "manual-input"})]
                             (conj (or (:environment-variables role) [])
                                   {:key env-current-key :value env-current-value-final}))
                           (:environment-variables role))

        ;; Add pending config file if exists
        updated-config-files (if has-pending-config?
                               (conj (or (:configuration-files role) [])
                                     {:key config-current-name :value config-current-content})
                               (:configuration-files role))]

    (-> role
        (assoc :environment-variables updated-env-vars)
        (assoc :configuration-files updated-config-files)
        ;; Remove temporary fields
        (dissoc :env-current-key :env-current-value)
        (dissoc :config-current-name :config-current-content))))

(defn inject-federation-fallback-secret
  "Emits the federation project as the role's static secret
  (CLOUDSDK_CORE_PROJECT). For gcp_iam it also emits the service-account key as
  GOOGLE_APPLICATION_CREDENTIALS, the fallback used when per-user federation
  can't mint credentials. gcp_oauth's admin credential is the OAuth client JSON
  (client_id + client_secret), which must never be written as a GCP credentials
  file, so no GOOGLE_APPLICATION_CREDENTIALS fallback is emitted for it."
  [role federation-form]
  (if (= (:connection-method role) "iam_federation")
    (let [provider (or (:builtin_provider federation-form) "gcp_iam")
          sa-json (:admin_credentials_json federation-form)
          project-id (get-in federation-form [:extra_config :project_id])
          with-project (assoc-in role [:metadata-credentials "CLOUDSDK_CORE_PROJECT"]
                                 {:value project-id :source "manual-input"})]
      (if (= provider "gcp_iam")
        (update with-project :configuration-files
                (fn [files]
                  (conj (vec (remove #(= (:key %) "GOOGLE_APPLICATION_CREDENTIALS") files))
                        {:key "GOOGLE_APPLICATION_CREDENTIALS" :value sa-json})))
        with-project))
    role))

(defn process-payload
  "Process the entire resource setup form into API payload"
  [db]
  (let [resource-name (get-in db [:resource-setup :name])
        resource-type (get-in db [:resource-setup :type])
        resource-subtype (get-in db [:resource-setup :subtype])
        agent-id (get-in db [:resource-setup :agent-id])
        federation-form (get-in db [:resources/federation :form])
        raw-roles (get-in db [:resource-setup :roles] [])
        ;; The connections metadata catalog (loaded from
        ;; /data/connections-metadata.json) is the source of truth for each
        ;; subtype's runtime command (e.g. BigQuery's
        ;; `bq query ... --project_id=$CLOUDSDK_CORE_PROJECT`). The setup
        ;; form only collects credentials; the command itself is never
        ;; surfaced to the user, so we have to inject it here. Without
        ;; this, the agent receives an empty CmdList and exec fails.
        resource-metadata (get-in db [:connections :metadata :data :connections] [])

        roles (mapv (fn [role]
                      (-> role
                          finalize-role-current-values
                          (inject-federation-fallback-secret federation-form)))
                    raw-roles)

        ;; Process all roles, injecting the command from the metadata
        ;; catalog for each role's subtype.
        processed-roles (mapv (fn [role]
                                (let [metadata (find-connection-metadata
                                                resource-metadata
                                                (:subtype role))
                                      command-role (get-in metadata [:resourceConfiguration :command])]
                                  (-> (process-role role agent-id command-role)
                                      (with-profile-attribute role db))))
                              roles)]

    {:name resource-name
     :type resource-type
     :subtype resource-subtype
     :agent_id agent-id
     :env_vars {}
     :roles processed-roles}))

