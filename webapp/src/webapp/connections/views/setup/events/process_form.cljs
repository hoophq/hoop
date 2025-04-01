(ns webapp.connections.views.setup.events.process-form
  (:require
   [clojure.string :as str]
   [webapp.connections.constants :as constants]
   [webapp.connections.helpers :as helpers]
   [webapp.connections.views.setup.tags-utils :as tags-utils]))

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
        effective-database-schema (or (:database-schema config) default-database-schema)

        command-string (get-in db [:connection-setup :command])
        payload {:type api-type
                 :subtype (when-not (= api-type "custom")
                            connection-subtype)
                 :name connection-name
                 :agent_id agent-id
                 :connection_tags tags
                 :tags old-tags
                 :secret secret
                 :command (if (= api-type "database")
                            []
                            (when-not (empty? command-string)
                              (or (re-seq #"'.*?'|\".*?\"|\S+|\t" command-string) [])))
                 :guardrail_rules guardrails-processed
                 :jira_issue_template_id jira-template-id-processed
                 :access_schema (or (when (= api-type "database")
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

(defn process-connection-for-update
  "Process an existing connection for the format used in the update form"
  [connection guardrails-list jira-templates-list]
  (let [credentials (process-connection-secret (:secret connection) "envvar")
        network-credentials (when (and (= (:type connection) "application")
                                       (= (:subtype connection) "tcp"))
                              (extract-network-credentials credentials))
        ;; Extrair tags no formato correto
        connection-tags (when-let [tags (:connection_tags connection)]
                          (cond
                            ;; Se for um mapa, converte para array de {:key k :value v}
                            (map? tags)
                            (mapv (fn [[k v]]
                                    (let [key (str (namespace k) "/" (name k))
                                          custom-key? (not (tags-utils/verify-tag-key key))
                                          parsed-key (if custom-key?
                                                       (name k)
                                                       key)]
                                      {:key parsed-key :value v :label (tags-utils/extract-label parsed-key)})) tags)

                            ;; Se for array simples, converte cada item para {:key item :value ""}
                            (sequential? tags)
                            (mapv (fn [tag]
                                    (if (map? tag)
                                      ;; Se já for no formato {:key k :value v}, usa diretamente
                                      tag
                                      ;; Senão, é um valor simples que vira chave
                                      {:key tag :value ""}))
                                  tags)

                            :else []))

        ;; Filtrar tags inválidas
        valid-tags (filter-valid-tags connection-tags)]

    {:type (:type connection)
     :subtype (:subtype connection)
     :name (:name connection)
     :agent-id (:agent_id connection)
     :database-credentials (when (= (:type connection) "database") credentials)
     :network-credentials network-credentials
     :command (if (empty? (:command connection))
                (get constants/connection-commands (:subtype connection))
                (str/join " " (:command connection)))
     :credentials {:environment-variables (when (= (:type connection) "custom")
                                            (process-connection-envvars (:secret connection) "envvar"))
                   :configuration-files (when (= (:type connection) "custom")
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
                                  "")}}))
