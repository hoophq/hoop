(ns webapp.connections.views.setup.events.process-form
  (:require
   [clojure.string :as str]
   [webapp.connections.constants :as constants]
   [webapp.connections.helpers :as helpers]
   [webapp.connections.views.setup.tags-utils :as tags-utils]))

(defn process-http-headers
  "Process HTTP headers by adding HEADER_ prefix to each key"
  [headers]
  (mapv (fn [{:keys [key value]}]
          {:key (str "HEADER_" key)
           :value value})
        headers))

;; Create a new connection
(defn get-api-connection-type [ui-type subtype]
  (cond
    (= subtype "ssh") "application"
    :else (case ui-type
            "network" "application"
            "server" "custom"
            "database" "database"
            "custom" "custom"
            "application" "application")))

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
  (filterv (fn [{:keys [key value label]}]
             (and key
                  (not (str/blank? (if (string? key) key (str key))))
                  value
                  (not (str/blank? (if (string? value) value (str value))))))
           tags))

(defn process-payload [db]
  (let [ui-type (get-in db [:connection-setup :type])
        connection-subtype (get-in db [:connection-setup :subtype])
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
        data-masking-types (get-in config [:data-masking-types])
        access-modes (get-in config [:access-modes])
        guardrails (get-in db [:connection-setup :config :guardrails])
        jira-template-id (get-in db [:connection-setup :config :jira-template-id])
        all-env-vars (cond
                       (= api-type "database")
                       (let [database-credentials (get-in db [:connection-setup :database-credentials])
                             credentials-as-env-vars (mapv (fn [[k v]]
                                                             {:key (name k)
                                                              :value v})
                                                           (seq database-credentials))]
                         (concat credentials-as-env-vars env-vars))

                       (= connection-subtype "tcp")
                       (let [network-credentials (get-in db [:connection-setup :network-credentials])
                             tcp-env-vars [{:key "HOST" :value (:host network-credentials)}
                                           {:key "PORT" :value (:port network-credentials)}]]
                         (concat tcp-env-vars env-vars))

                       (= connection-subtype "httpproxy")
                       (let [network-credentials (get-in db [:connection-setup :network-credentials])
                             insecure-value (if (:insecure network-credentials) "true" "false")
                             http-env-vars [{:key "REMOTE_URL" :value (:remote_url network-credentials)}
                                            {:key "INSECURE" :value insecure-value}]
                             headers (get-in db [:connection-setup :credentials :environment-variables] [])
                             processed-headers (process-http-headers headers)]
                         (concat http-env-vars processed-headers))

                       (= connection-subtype "ssh")
                       (let [ssh-credentials (get-in db [:connection-setup :ssh-credentials])
                             ssh-env-vars (filterv #(not (str/blank? (:value %)))
                                                   [{:key "HOST" :value (get ssh-credentials "host")}
                                                    {:key "PORT" :value (get ssh-credentials "port")}
                                                    {:key "USER" :value (get ssh-credentials "user")}
                                                    {:key "PASS" :value (get ssh-credentials "pass")}
                                                    {:key "AUTHORIZED_SERVER_KEYS" :value (get ssh-credentials "authorized_server_keys")}])]
                         (concat ssh-env-vars env-vars))

                       :else env-vars)

        secret (clj->js
                (merge
                 (helpers/config->json all-env-vars "envvar:")
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
        command-array (if (seq command-args)
                        (mapv #(get % "value") command-args)
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
                 :agent_id agent-id
                 :connection_tags tags
                 :tags old-tags
                 :secret secret
                 :command (if (= api-type "database")
                            []
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
                              (mapv #(get % "value") review-groups))}]

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
                                       (str/replace secret-start-name "")
                                       str/lower-case)]
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
                                       (str/replace secret-start-name ""))]
                     (conj acc {:key clean-key :value (decode-base64 v)}))
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
  "Retrieves HOST and PORT from secrets for network credentials"
  [credentials]
  {:host (get credentials "host")
   :port (get credentials "port")})

(defn extract-ssh-credentials
  "Retrieves HOST, PORT, USER, PASS and AUTHORIZED_SERVER_KEYS from secrets for ssh credentials"
  [credentials]
  {"host" (get credentials "host")
   "port" (get credentials "port")
   "user" (get credentials "user")
   "pass" (get credentials "pass")
   "authorized_server_keys" (get credentials "authorized_server_keys")})

(defn extract-http-credentials
  "Retrieves remote_url and insecure flag from secrets for http credentials"
  [credentials]
  {:remote_url (get credentials "remote_url")
   :insecure (= (get credentials "insecure") "true")})

(defn process-connection-for-update
  "Process an existing connection for the format used in the update form"
  [connection guardrails-list jira-templates-list]
  (let [credentials (process-connection-secret (:secret connection) "envvar")
        network-credentials (when (and (= (:type connection) "application")
                                       (= (:subtype connection) "tcp"))
                              (extract-network-credentials credentials))
        http-credentials (when (and (= (:type connection) "application")
                                    (= (:subtype connection) "httpproxy"))
                           (extract-http-credentials credentials))
        ssh-credentials (when (and (= (:type connection) "application")
                                   (= (:subtype connection) "ssh"))
                          (extract-ssh-credentials credentials))
        ssh-auth-method (when ssh-credentials
                          (cond
                            (and (not (empty? (get ssh-credentials "authorized_server_keys")))
                                 (empty? (get ssh-credentials "pass"))) "key"
                            (and (not (empty? (get ssh-credentials "pass")))
                                 (empty? (get ssh-credentials "authorized_server_keys"))) "password"
                            (not (empty? (get ssh-credentials "authorized_server_keys"))) "key"
                            :else "password"))
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

        http-env-vars (when (and (= (:type connection) "application")
                                 (= (:subtype connection) "httpproxy"))
                        (let [headers (process-connection-envvars (:secret connection) "envvar")
                              remote-url? #(= (:key %) "REMOTE_URL")
                              header? #(str/starts-with? % "HEADER_")
                              processed-headers (map (fn [{:keys [key value]}]
                                                       {:key (if (header? key)
                                                               (str/replace key "HEADER_" "")
                                                               key)
                                                        :value value})
                                                     (remove remote-url? headers))]
                          processed-headers))]

    (let [connection-type (:type connection)
          connection-subtype (:subtype connection)
          is-custom-with-override? (and (= connection-type "custom")
                                        (contains? #{"dynamodb" "cloudwatch"} connection-subtype))
          resource-subtype-override (when is-custom-with-override? connection-subtype)]
      {:type connection-type
       :subtype (if is-custom-with-override? "custom" connection-subtype)
       :name (:name connection)
       :agent-id (:agent_id connection)
       :resource-subtype-override resource-subtype-override
       :database-credentials (when (= connection-type "database") credentials)
       :network-credentials (or network-credentials http-credentials)
       :ssh-credentials ssh-credentials
       :ssh-auth-method (or ssh-auth-method "password")
       :command (if (empty? (:command connection))
                  (get constants/connection-commands connection-subtype)
                  (str/join " " (:command connection)))
       :command-args (if (empty? (:command connection))
                       []
                       (mapv #(hash-map "value" % "label" %) (:command connection)))
       :credentials {:environment-variables (cond
                                              (= connection-type "custom")
                                              (process-connection-envvars (:secret connection) "envvar")

                                              (and (= connection-type "application")
                                                   (= connection-subtype "httpproxy"))
                                              http-env-vars

                                              :else [])
                     :configuration-files (when (= connection-type "custom")
                                            (process-connection-envvars (:secret connection) "filesystem"))}
       :tags {:data valid-tags}
       :old-tags (:tags connection)

       :config {:review (seq (:reviewers connection))
                :review-groups (mapv #(hash-map "value" % "label" %) (:reviewers connection))
                :data-masking (:redact_enabled connection)
                :data-masking-types (if (:redact_enabled connection)
                                      (mapv #(hash-map "value" % "label" %) (:redact_types connection))
                                      [])
                :database-schema (= (:access_schema connection) "enabled")
                :access-modes {:runbooks (= (:access_mode_runbooks connection) "enabled")
                               :native (= (:access_mode_connect connection) "enabled")
                               :web (= (:access_mode_exec connection) "enabled")}
                :guardrails (if (empty? (:guardrail_rules connection))
                              []
                              (transform-filtered-guardrails-selected
                               guardrails-list
                               (:guardrail_rules connection)))
                :jira-template-id (if (:jira_issue_template_id connection)
                                    (transform-filtered-jira-template-selected
                                     jira-templates-list
                                     (:jira_issue_template_id connection))
                                    "")}})))
