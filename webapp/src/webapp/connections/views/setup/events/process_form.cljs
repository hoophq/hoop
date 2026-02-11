(ns webapp.connections.views.setup.events.process-form
  (:require
   [clojure.string :as str]
   [webapp.connections.constants :as constants]
   [webapp.connections.helpers :as helpers]
   [webapp.resources.helpers :refer [get-secret-prefix]]
   [webapp.resources.constants :refer [http-proxy-subtypes]]
   [webapp.resources.setup.events.process-form :as resource-process-form]
   [webapp.connections.views.setup.tags-utils :as tags-utils]
   [webapp.connections.views.setup.connection-method :as connection-method]))

(defn process-http-headers
  "Process HTTP headers by adding HEADER_ prefix to each key"
  [headers connection-method secrets-provider]
  (mapv (fn [{:keys [key value]}]
          {:key (str "HEADER_" key)
           :value (resource-process-form/extract-value value connection-method (keyword key) secrets-provider)})
        headers))

;; Create a new connection
(defn get-api-connection-type [ui-type subtype]
  (cond
    (or (= subtype "ssh")
        (= subtype "git")
        (= subtype "github")) "application"
    :else (case ui-type
            "httpproxy" "httpproxy"
            "network" "application"
            "server" "custom"
            "database" "database"
            "custom" "custom"
            "application" "application"
            "httpproxy" "httpproxy")))

(defn tags-array->map
  "Convert an array of tags [{:key k :value v}] to a map {k v}
   Ignores tags with empty keys or values"
  [tags]
  (reduce (fn [acc {:keys [key value]}]
            (if (and key
                     (not (str/blank? (if (string? key) key (str key))))
                     value
                     (not (str/blank? (if (string? value) value (str value)))))
              (assoc acc key (or value ""))
              acc))
          {}
          tags))

(defn filter-valid-tags
  "Remove tags que possuem key ou value vazios"
  [tags]
  (filterv (fn [{:keys [key value]}]
             (and key
                  (not (str/blank? (if (string? key) key (str key))))
                  value
                  (not (str/blank? (if (string? value) value (str value))))))
           tags))

(defn process-payload [db & [resource-name]]
  (let [ui-type (get-in db [:connection-setup :type])
        connection-subtype (get-in db [:connection-setup :subtype])
        is-http-proxy-subtype? (contains? http-proxy-subtypes connection-subtype)
        api-type (get-api-connection-type ui-type connection-subtype)
        connection-name (get-in db [:connection-setup :name])
        agent-id (get-in db [:connection-setup :agent-id])
        old-tags (get-in db [:connection-setup :old-tags] [])
        tags-array (get-in db [:connection-setup :tags :data] [])
        filtered-tags (filter-valid-tags tags-array)
        tags (tags-array->map filtered-tags)
        config (get-in db [:connection-setup :config])
        env-vars (get-in db [:connection-setup :credentials :environment-variables] [])
        config-files (get-in db [:connection-setup :credentials :configuration-files] [])
        review-groups (get-in config [:review-groups])
        min-review-approvals (get-in config [:min-review-approvals])
        force-approve-groups (get-in config [:force-approve-groups])
        access-max-duration (get-in config [:access-max-duration])
        data-masking-types (get-in config [:data-masking-types])
        access-modes (get-in config [:access-modes])
        guardrails (get-in db [:connection-setup :config :guardrails])
        jira-template-id (get-in db [:connection-setup :config :jira-template-id])
        metadata-credentials (get-in db [:connection-setup :metadata-credentials])
        connection-method (get-in db [:connection-setup :connection-method] "manual-input")
        secrets-provider (get-in db [:connection-setup :secrets-manager-provider] "vault-kv1")
        all-env-vars (cond
                       (= connection-subtype "kubernetes-token")
                       (let [kubernetes-token (get-in db [:connection-setup :kubernetes-token])
                             cluster-url-value (get kubernetes-token :cluster_url)
                             auth-value (get kubernetes-token :authorization)
                             insecure-value (get kubernetes-token :insecure)
                             cluster-url (resource-process-form/extract-value cluster-url-value connection-method :cluster_url secrets-provider)
                             auth (resource-process-form/extract-value auth-value connection-method :authorization secrets-provider)
                             auth-source (if (map? auth-value)
                                           (:source auth-value)
                                           (when (= connection-method "secrets-manager")
                                             secrets-provider))
                             is-manual-input? (= auth-source "manual-input")
                             auth-with-bearer (if is-manual-input?
                                                (if (str/starts-with? auth "Bearer ")
                                                  auth
                                                  (str "Bearer " auth))
                                                auth)
                             kubernetes-token-env-vars (filterv #(not (str/blank? (:value %)))
                                                                [{:key "REMOTE_URL" :value cluster-url}
                                                                 {:key "HEADER_AUTHORIZATION" :value auth-with-bearer}
                                                                 {:key "INSECURE" :value (if (map? insecure-value)
                                                                                           (:value insecure-value)
                                                                                           (str insecure-value))}])]
                         kubernetes-token-env-vars)

                       (and (or (= ui-type "custom") (= ui-type "database"))
                            connection-subtype
                            (not= connection-subtype "linux-vm")
                            (seq metadata-credentials))
                       (let [connection-method (get-in db [:connection-setup :connection-method] "manual-input")
                             is-aws-iam-role? (= connection-method "aws-iam-role")
                             ;; For AWS IAM Role, always ensure PASS field is set to "authtoken"
                             metadata-credentials-with-pass (if is-aws-iam-role?
                                                              (let [pass-key (or (first (filter #(= (str/lower-case (name %)) "pass") (keys metadata-credentials)))
                                                                                 "PASS")]
                                                                (assoc (or metadata-credentials {}) pass-key {:value "authtoken" :source "aws-iam-role"}))
                                                              metadata-credentials)
                             credentials-as-env-vars (mapv (fn [[field-key field-value]]
                                                             (let [{:keys [value source]} (connection-method/normalize-credential-value field-value)
                                                                   field-key-lower (str/lower-case (name field-key))
                                                                   is-user-or-pass? (or (= field-key-lower "user") (= field-key-lower "pass"))
                                                                   prefix (when source (get-secret-prefix source))
                                                                   final-value (cond
                                                                                 ;; AWS IAM Role: apply _aws_iam_rds: prefix to user/pass
                                                                                 (and is-aws-iam-role? is-user-or-pass?)
                                                                                 (str "_aws_iam_rds:" value)
                                                                                 ;; For non-AWS IAM Role, apply prefix if present
                                                                                 (and (not is-aws-iam-role?) (seq prefix))
                                                                                 (str prefix value)
                                                                                 :else
                                                                                 value)]
                                                               {:key (name field-key)
                                                                :value final-value}))
                                                           (seq metadata-credentials-with-pass))]
                         credentials-as-env-vars)

                       (= connection-subtype "tcp")
                       (let [network-credentials (get-in db [:connection-setup :network-credentials])
                             host-value (get network-credentials :host)
                             port-value (get network-credentials :port)
                             host (resource-process-form/extract-value host-value connection-method :host secrets-provider)
                             port (resource-process-form/extract-value port-value connection-method :port secrets-provider)
                             tcp-env-vars (filterv #(not (str/blank? (:value %)))
                                                   [{:key "HOST" :value host}
                                                    {:key "PORT" :value port}])
                             processed-env-vars (mapv (fn [{:keys [key value]}]
                                                        {:key key
                                                         :value (resource-process-form/extract-value value connection-method (keyword key) secrets-provider)})
                                                      env-vars)]
                         (concat tcp-env-vars processed-env-vars))

                       is-http-proxy-subtype?
                       (let [;; Claude Code uses claude-code-credentials, other http proxies use network-credentials
                             credentials (if (= connection-subtype "claude-code")
                                           (get-in db [:connection-setup :claude-code-credentials])
                                           (get-in db [:connection-setup :network-credentials]))
                             remote-url-value (get credentials :remote_url)
                             insecure-value (get credentials :insecure)
                             remote-url (resource-process-form/extract-value remote-url-value connection-method :remote_url secrets-provider)
                             insecure-str (if (map? insecure-value)
                                            (:value insecure-value)
                                            (if (boolean? insecure-value)
                                              (str insecure-value)
                                              (if insecure-value "true" "false")))

                             ;; For claude-code, include the HEADER_X_API_KEY in the base env vars
                             base-env-vars (if (= connection-subtype "claude-code")
                                             (let [api-key-value (get credentials :HEADER_X_API_KEY)
                                                   api-key (resource-process-form/extract-value api-key-value connection-method :HEADER_X_API_KEY secrets-provider)]
                                               (filterv #(not (str/blank? (:value %)))
                                                        [{:key "REMOTE_URL" :value remote-url}
                                                         {:key "HEADER_X_API_KEY" :value api-key}
                                                         {:key "INSECURE" :value insecure-str}]))
                                             (filterv #(not (str/blank? (:value %)))
                                                      [{:key "REMOTE_URL" :value remote-url}
                                                       {:key "INSECURE" :value insecure-str}]))

                             headers (get-in db [:connection-setup :credentials :environment-variables] [])
                             processed-headers (process-http-headers headers connection-method secrets-provider)]
                         (concat base-env-vars processed-headers))


                       (or (= connection-subtype "ssh")
                           (= connection-subtype "git")
                           (= connection-subtype "github"))
                       (let [ssh-credentials (get-in db [:connection-setup :ssh-credentials])
                             host-value (get ssh-credentials "host")
                             port-value (get ssh-credentials "port")
                             user-value (get ssh-credentials "user")
                             pass-value (get ssh-credentials "pass")
                             keys-value (get ssh-credentials "authorized_server_keys")
                             ssh-env-vars (filterv #(not (str/blank? (:value %)))
                                                   [{:key "HOST" :value (resource-process-form/extract-value host-value connection-method "host" secrets-provider)}
                                                    {:key "PORT" :value (resource-process-form/extract-value port-value connection-method "port" secrets-provider)}
                                                    {:key "USER" :value (resource-process-form/extract-value user-value connection-method "user" secrets-provider)}
                                                    {:key "PASS" :value (resource-process-form/extract-value pass-value connection-method "pass" secrets-provider)}
                                                    {:key "AUTHORIZED_SERVER_KEYS" :value (resource-process-form/extract-value keys-value connection-method "authorized_server_keys" secrets-provider)}])]
                         (concat ssh-env-vars env-vars))

                       :else (mapv (fn [{:keys [key value]}]
                                     {:key key
                                      :value (resource-process-form/extract-value value connection-method (keyword key) secrets-provider)})
                                   env-vars))

        secret (clj->js
                (merge
                 (helpers/config->json all-env-vars "envvar:" connection-subtype)
                 (when (seq config-files)
                   (helpers/config->json config-files "filesystem:"))))

        guardrails-processed (mapv #(get % "value") guardrails)
        jira-template-id-processed (when jira-template-id
                                     (get jira-template-id "value"))

        default-access-modes {:runbooks true :native true :web true}
        effective-access-modes (merge default-access-modes access-modes)

        default-database-schema true
        effective-database-schema (if (nil? (:database-schema config))
                                    default-database-schema
                                    (:database-schema config))

        command-string (get-in db [:connection-setup :command])
        command-args (get-in db [:connection-setup :command-args] [])
        command-args-from-metadata (get-in db [:connection-setup :metadata-command-args] [])
        command-array (if (seq command-args)
                        (mapv #(get % "value") command-args)

                        ;; Deprecated
                        (when-not (empty? command-string)
                          (or (re-seq #"'.*?'|\".*?\"|\S+|\t" command-string) [])))
        resource-subtype-override (get-in db [:connection-setup :resource-subtype-override])
        effective-subtype (if (and (= ui-type "custom")
                                   resource-subtype-override
                                   (seq resource-subtype-override))
                            resource-subtype-override
                            connection-subtype)
        payload {:type api-type
                 :subtype effective-subtype
                 :name connection-name
                 :resource_name resource-name
                 :agent_id agent-id
                 :connection_tags tags
                 :tags old-tags
                 :secret secret
                 :command (cond
                            (= api-type "database") []
                            (seq command-args-from-metadata) command-args-from-metadata
                            :else
                            command-array)
                 :guardrail_rules guardrails-processed
                 :jira_issue_template_id jira-template-id-processed
                 :access_schema (or (when (or (= api-type "database")
                                              (= connection-subtype "dynamodb")
                                              (= connection-subtype "cloudwatch"))
                                      (if effective-database-schema
                                        "enabled"
                                        "disabled"))
                                    "disabled")
                 :access_mode_runbooks (if (:runbooks effective-access-modes) "enabled" "disabled")
                 :access_mode_exec (if (:web effective-access-modes) "enabled" "disabled")
                 :access_mode_connect (if (:native effective-access-modes) "enabled" "disabled")
                 :redact_enabled true
                 :redact_types (if (:data-masking config)
                                 (mapv #(get % "value") data-masking-types)
                                 [])
                 :reviewers (when (and (:review config) (seq review-groups))
                              (mapv #(get % "value") review-groups))
                 :min_review_approvals (when (and (:review config) min-review-approvals)
                                         min-review-approvals)
                 :force_approve_groups (when (and (:review config) (seq force-approve-groups))
                                         (mapv #(get % "value") force-approve-groups))
                 :access_max_duration (when access-max-duration
                                        (if (number? access-max-duration)
                                          access-max-duration
                                          (js/parseInt access-max-duration 10)))}]

    payload))

;; Update an existing connection
(defn is-base64? [str]
  (try
    (boolean (re-matches #"^[A-Za-z0-9+/]*={0,2}$" str))
    (catch js/Error _
      false)))

(defn decode-base64 [s]
  (if (is-base64? s)
    (try
      (-> s
          js/atob
          js/decodeURIComponent)
      (catch js/Error _
        s))
    s))

(defn process-connection-secret
  "Process the secret values of the connection from base64 to string"
  [secret secret-type]
  (let [secret-start-name (if (= secret-type "envvar")
                            "envvar:"
                            "filesystem:")]
    (reduce-kv (fn [acc k v]
                 (if (str/starts-with? (name k) secret-start-name)
                   (let [clean-key (-> (name k)
                                       (str/replace secret-start-name ""))]
                     (assoc acc clean-key (decode-base64 v)))
                   acc))
               {}
               secret)))

(defn process-connection-envvars
  "Process the secret values of the connection from base64 to string"
  [secret secret-type]
  (let [secret-start-name (if (= secret-type "envvar")
                            "envvar:"
                            "filesystem:")]
    (reduce-kv (fn [acc k v]
                 (if (str/starts-with? (name k) secret-start-name)
                   (let [clean-key (-> (name k)
                                       (str/replace secret-start-name ""))
                         decoded-value (decode-base64 v)
                         normalized (connection-method/normalize-credential-value decoded-value)]
                     (conj acc {:key clean-key :value normalized}))
                   acc))
               []
               secret)))

(defn transform-filtered-guardrails-selected [guardrails connection-guardrail-ids]
  (->> guardrails
       (filter #(some #{(:id %)} connection-guardrail-ids))
       (mapv (fn [{:keys [id name]}]
               {"value" id
                "label" name}))))

(defn transform-filtered-jira-template-selected [jira-templates jira-template-id]
  (first
   (->> jira-templates
        (filter #(= (:id %) jira-template-id))
        (mapv (fn [{:keys [id name]}]
                {"value" id
                 "label" name})))))

(defn extract-network-credentials
  "Retrieves and normalizes HOST and PORT from secrets for network credentials"
  [credentials]
  (let [host-value (get credentials "HOST")
        port-value (get credentials "PORT")
        normalized-host (connection-method/normalize-credential-value host-value)
        normalized-port (connection-method/normalize-credential-value port-value)]
    {:host normalized-host
     :port normalized-port}))

(defn extract-ssh-credentials
  "Retrieves and normalizes HOST, PORT, USER, PASS and AUTHORIZED_SERVER_KEYS from secrets for ssh credentials"
  [credentials]
  (let [host-value (get credentials "HOST")
        port-value (get credentials "PORT")
        user-value (get credentials "USER")
        pass-value (get credentials "PASS")
        keys-value (get credentials "AUTHORIZED_SERVER_KEYS")
        auth-method-value (get credentials "AUTH-METHOD")]
    {"host" (connection-method/normalize-credential-value host-value)
     "port" (connection-method/normalize-credential-value port-value)
     "user" (connection-method/normalize-credential-value user-value)
     "pass" (connection-method/normalize-credential-value pass-value)
     "authorized_server_keys" (connection-method/normalize-credential-value keys-value)
     "auth-method" (connection-method/normalize-credential-value auth-method-value)}))

(defn extract-http-credentials
  "Retrieves and normalizes remote_url and insecure flag from secrets for http credentials"
  [credentials]
  (let [remote-url-value (get credentials "REMOTE_URL")
        normalized-remote-url (connection-method/normalize-credential-value remote-url-value)
        insecure-value (get credentials "INSECURE")]
    {:remote_url normalized-remote-url
     :insecure (if (string? insecure-value)
                 (= insecure-value "true")
                 (boolean insecure-value))}))

(defn extract-kubernetes-token-credentials
  "Retrieves and normalizes remote_url, authorization and insecure flag from secrets for kubernetes credentials"
  [credentials]
  (let [auth-header (get credentials "HEADER_AUTHORIZATION" "")
        auth-value (if (str/starts-with? auth-header "Bearer ")
                     (subs auth-header 7)
                     auth-header)
        cluster-url-value (get credentials "REMOTE_URL")
        normalized-cluster-url (connection-method/normalize-credential-value cluster-url-value)
        normalized-authorization (connection-method/normalize-credential-value auth-value)
        insecure-value (get credentials "INSECURE")]
    {:cluster_url normalized-cluster-url
     :authorization normalized-authorization
     :insecure (if (string? insecure-value)
                 (= insecure-value "true")
                 (boolean insecure-value))}))

(defn extract-claude-code-credentials
  "Retrieves and normalizes remote_url, API key and insecure flag from secrets for claude-code credentials"
  [credentials]
  (let [remote-url-value (get credentials "REMOTE_URL")
        normalized-remote-url (connection-method/normalize-credential-value remote-url-value)
        api-key-value (get credentials "HEADER_X_API_KEY")
        normalized-api-key (connection-method/normalize-credential-value api-key-value)
        insecure-value (get credentials "INSECURE")]
    {:remote_url normalized-remote-url
     :HEADER_X_API_KEY normalized-api-key
     :insecure (if (string? insecure-value)
                 (= insecure-value "true")
                 (boolean insecure-value))}))

(defn process-connection-for-update
  "Process an existing connection for the format used in the update form"
  [connection guardrails-list jira-templates-list]
  (let [connection-type (:type connection)
        connection-subtype (:subtype connection)
        is-http-proxy-subtype? (contains? http-proxy-subtypes connection-subtype)
        credentials (process-connection-secret (:secret connection) "envvar")

        is-metadata-driven? (and (= connection-type "custom")
                                 (not (contains? #{"tcp" "ssh" "linux-vm"}
                                                 connection-subtype))
                                 (not is-http-proxy-subtype?))

        network-credentials (when (and (= connection-type "application")
                                       (= connection-subtype "tcp"))
                              (extract-network-credentials credentials))
        ;; Deprecated: we are moving to httpproxy being its own connection type, not a subtype
        ;; of application. This is needed to render older forms saved in that format.
        http-credentials-deprecated (when (and (= connection-type "application")
                                               is-http-proxy-subtype?)
                                      (extract-http-credentials credentials))

        http-credentials (when (= connection-type "httpproxy")
                           (extract-http-credentials credentials))
        ssh-credentials (when (and (= connection-type "application")
                                   (or (= connection-subtype "ssh")
                                       (= connection-subtype "git")
                                       (= connection-subtype "github")))
                          (extract-ssh-credentials credentials))
        kubernetes-token (when (and (= connection-type "custom")
                                    (= connection-subtype "kubernetes-token"))
                           (extract-kubernetes-token-credentials credentials))
        claude-code-credentials (when (and (= connection-type "httpproxy")
                                           (= connection-subtype "claude-code"))
                                  (extract-claude-code-credentials credentials))
        ssh-auth-method (when ssh-credentials
                          (let [keys-cred (get ssh-credentials "authorized_server_keys")
                                pass-cred (get ssh-credentials "pass")
                                keys-value (if (map? keys-cred)
                                             (:value keys-cred)
                                             keys-cred)
                                pass-value (if (map? pass-cred)
                                             (:value pass-cred)
                                             pass-cred)
                                has-keys? (and keys-value (not (str/blank? (str keys-value))))
                                has-pass? (and pass-value (not (str/blank? (str pass-value))))]
                            (cond
                              (and has-keys? (not has-pass?)) "key"
                              (and has-pass? (not has-keys?)) "password"
                              has-keys? "key"
                              :else "password")))

        ;; Infer connection method from SSH credentials
        ssh-connection-info (when (seq ssh-credentials)
                              (connection-method/infer-connection-method ssh-credentials))

        ;; Infer connection method from Kubernetes token
        kubernetes-connection-info (when (seq kubernetes-token)
                                     (connection-method/infer-connection-method kubernetes-token))

        ;; Infer connection method from network credentials (TCP)
        network-connection-info (when (seq network-credentials)
                                  (connection-method/infer-connection-method network-credentials))

        ;; Infer connection method from Claude Code credentials
        claude-code-connection-info (when (seq claude-code-credentials)
                                      (connection-method/infer-connection-method claude-code-credentials))

        connection-tags (when-let [tags (:connection_tags connection)]
                          (cond
                            (map? tags)
                            (mapv (fn [[k v]]
                                    (let [key (str (namespace k) "/" (name k))
                                          custom-key? (not (tags-utils/verify-tag-key key))
                                          parsed-key (if custom-key?
                                                       (name k)
                                                       key)]
                                      {:key parsed-key :value v :label (tags-utils/extract-label parsed-key)})) tags)

                            (sequential? tags)
                            (mapv (fn [tag]
                                    (if (map? tag)
                                      tag
                                      {:key tag :value ""}))
                                  tags)

                            :else []))

        valid-tags (filter-valid-tags connection-tags)

        http-env-vars (when (or (and (= (:type connection) "application")
                                     is-http-proxy-subtype?)
                                (= (:type connection) "httpproxy"))
                        (let [headers (process-connection-envvars (:secret connection) "envvar")
                              remote-url? #(= (:key %) "REMOTE_URL")
                              insecure? #(= (:key %) "INSECURE")
                              header? #(str/starts-with? % "HEADER_")
                              processed-headers (mapv (fn [{:keys [key value]}]
                                                        {:key (if (header? key)
                                                                (str/replace key "HEADER_" "")
                                                                key)
                                                         :value value})
                                                      (->> headers
                                                           (remove remote-url?)
                                                           (remove insecure?)))]
                          processed-headers))

        connection-type (:type connection)
        connection-subtype (:subtype connection)
        is-custom-with-override? (and (= connection-type "custom")
                                      (contains? #{"dynamodb" "cloudwatch"} connection-subtype))
        resource-subtype-override (when is-custom-with-override? connection-subtype)

        needs-normalization? (or (= connection-type "database")
                                 is-metadata-driven?)
        normalized-credentials (when needs-normalization?
                                 (connection-method/normalize-credentials credentials))
        config-files-raw (when (or (= connection-type "custom")
                                   (= connection-type "database"))
                           (process-connection-envvars (:secret connection) "filesystem"))
        normalized-config-files (when config-files-raw
                                  (mapv (fn [{:keys [key value]}]
                                          {:key key
                                           :value (if (map? value)
                                                    (let [inner-value (:value value)]
                                                      (if (map? inner-value)
                                                        (str (:value inner-value))
                                                        (str inner-value)))
                                                    (str value))})
                                        config-files-raw))

        env-vars-connection-info (when (= connection-type "custom")
                                   (let [env-vars-to-check (process-connection-envvars (:secret connection) "envvar")
                                         env-vars-map (reduce (fn [acc {:keys [key value]}]
                                                                (if (map? value)
                                                                  (assoc acc (keyword key) value)
                                                                  (assoc acc (keyword key) {:value (str value) :source "manual-input"})))
                                                              {}
                                                              env-vars-to-check)]
                                     (when (seq env-vars-map)
                                       (connection-method/infer-connection-method env-vars-map))))

        http-connection-info (when (and (= connection-type "application")
                                        is-http-proxy-subtype?
                                        (or (seq http-credentials-deprecated) (seq http-env-vars)))
                               (let [env-vars-map (when (seq http-env-vars)
                                                    (reduce (fn [acc {:keys [key value]}]
                                                              (if (map? value)
                                                                (assoc acc (keyword key) value)
                                                                (assoc acc (keyword key) {:value (str value) :source "manual-input"})))
                                                            {}
                                                            http-env-vars))
                                     combined-credentials (merge http-credentials-deprecated env-vars-map)]
                                 (when (seq combined-credentials)
                                   (connection-method/infer-connection-method combined-credentials))))

        http-proxy-connection-info (when (and (= connection-type "httpproxy")
                                              (seq http-credentials))
                                     (connection-method/infer-connection-method http-credentials))

        inferred-connection-info (cond
                                   (seq normalized-credentials)
                                   (connection-method/infer-connection-method normalized-credentials)

                                   http-connection-info
                                   http-connection-info

                                   http-proxy-connection-info
                                   http-proxy-connection-info

                                   env-vars-connection-info
                                   env-vars-connection-info

                                   ssh-connection-info
                                   ssh-connection-info

                                   kubernetes-connection-info
                                   kubernetes-connection-info

                                   network-connection-info
                                   network-connection-info

                                   claude-code-connection-info
                                   claude-code-connection-info

                                   :else
                                   {:connection-method "manual-input"
                                    :secrets-manager-provider nil})]

    {:type connection-type
     :subtype (if is-custom-with-override? "custom" connection-subtype)
     :name (:name connection)
     :resource-name (:resource_name connection)
     :agent-id (:agent_id connection)
     :resource-subtype-override resource-subtype-override
     :database-credentials (when (= connection-type "database")
                             (or normalized-credentials credentials))
     :metadata-credentials (when (or (= connection-type "database")
                                     (and (or (= connection-type "custom") (= connection-type "database"))
                                          connection-subtype
                                          (seq (or normalized-credentials credentials))))
                             (or normalized-credentials credentials))
     :connection-method (if inferred-connection-info
                          (:connection-method inferred-connection-info)
                          "manual-input")
     :secrets-manager-provider (when inferred-connection-info
                                 (:secrets-manager-provider inferred-connection-info))
     :network-credentials (or network-credentials http-credentials http-credentials-deprecated)
     :ssh-credentials ssh-credentials
     :kubernetes-token kubernetes-token
     :claude-code-credentials claude-code-credentials
     :ssh-auth-method (or ssh-auth-method "password")
     :command (if (empty? (:command connection))
                (get constants/connection-commands connection-subtype)
                (str/join " " (:command connection)))
     :command-args (if (empty? (:command connection))
                     []
                     (mapv #(hash-map "value" % "label" %) (:command connection)))
     :configuration-files (or normalized-config-files
                              (when (or (= connection-type "custom")
                                        (= connection-type "database"))
                                (process-connection-envvars (:secret connection) "filesystem")))
     :credentials {:environment-variables (cond
                                            (= connection-type "custom")
                                            (process-connection-envvars (:secret connection) "envvar")

                                            (or (and (= connection-type "application")
                                                     is-http-proxy-subtype?)
                                                (= connection-type "httpproxy"))
                                            http-env-vars

                                            :else [])
                   :configuration-files (or normalized-config-files
                                            (when (or (= connection-type "custom")
                                                      (= connection-type "database"))
                                              (process-connection-envvars (:secret connection) "filesystem")))}
     :tags {:data valid-tags}
     :old-tags (:tags connection)

     :config {:review (seq (:reviewers connection))
              :review-groups (mapv #(hash-map "value" % "label" %) (:reviewers connection))
              :min-review-approvals (:min_review_approvals connection)
              :force-approve-groups (if (seq (:force_approve_groups connection))
                                      (mapv #(hash-map "value" % "label" %) (:force_approve_groups connection))
                                      [])
              :access-max-duration (:access_max_duration connection)
              :data-masking (:redact_enabled connection)
              :data-masking-types (if (:redact_enabled connection)
                                    (mapv #(hash-map "value" % "label" %) (:redact_types connection))
                                    [])
              :database-schema (= (:access_schema connection) "enabled")
              :access-modes {:runbooks (= (:access_mode_runbooks connection) "enabled")
                             :native (= (:access_mode_connect connection) "enabled")
                             :web (= (:access_mode_exec connection) "enabled")}
              :guardrails (if (seq (:guardrail_rules connection))
                            (transform-filtered-guardrails-selected
                             guardrails-list
                             (:guardrail_rules connection))
                            [])
              :jira-template-id (if (:jira_issue_template_id connection)
                                  (transform-filtered-jira-template-selected
                                   jira-templates-list
                                   (:jira_issue_template_id connection))
                                  "")}}))
