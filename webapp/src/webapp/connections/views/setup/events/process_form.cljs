(ns webapp.connections.views.setup.events.process-form
  (:require
   [clojure.string :as str]
   [webapp.connections.constants :as constants]
   [webapp.connections.helpers :as helpers]))

;; Create a new connection
(defn get-api-connection-type [ui-type]
  (case ui-type
    "network" "application"
    "server" "custom"
    "database" "database"
    "custom" "custom"
    "application" "application"))

(defn tags-array->map
  "Converte um array de tags [{:key k :value v}] para um map {k v}"
  [tags]
  (reduce (fn [acc {:keys [key value]}]
            (assoc acc key (or value "")))
          {}
          tags))

(defn tags-map->array
  "Converte um map de tags {k v} para um array [{:key k :value v}]"
  [tags]
  (mapv (fn [[k v]]
          {:key k :value v})
        tags))



(defn process-payload [db]
  (let [ui-type (get-in db [:connection-setup :type])
        connection-subtype (get-in db [:connection-setup :subtype])
        connection-name (get-in db [:connection-setup :name])
        agent-id (get-in db [:connection-setup :agent-id])
        tags-array (get-in db [:connection-setup :tags] [])
        tags (tags-array->map tags-array)
        config (get-in db [:connection-setup :config])
        env-vars (get-in db [:connection-setup :credentials :environment-variables] [])
        config-files (get-in db [:connection-setup :credentials :configuration-files] [])
        review-groups (get-in config [:review-groups])
        data-masking-types (get-in config [:data-masking-types])
        access-modes (get-in config [:access-modes])
        guardrails (get-in db [:connection-setup :guardrails])
        jira-template-id (get-in db [:connection-setup :jira-template-id])

        ;; Mapeamento do tipo da UI para o tipo da API
        api-type (get-api-connection-type ui-type)

        ;; Processamento de credenciais baseado no tipo
        all-env-vars (cond
                              ;; Para bancos de dados
                       (= api-type "database")
                       (let [database-credentials (get-in db [:connection-setup :database-credentials])
                             credentials-as-env-vars (mapv (fn [[k v]]
                                                             {:key (name k)
                                                              :value v})
                                                           (seq database-credentials))]
                         (concat credentials-as-env-vars env-vars))

                              ;; Para TCP
                       (= connection-subtype "tcp")
                       (let [network-credentials (get-in db [:connection-setup :network-credentials])
                             tcp-env-vars [{:key "HOST" :value (:host network-credentials)}
                                           {:key "PORT" :value (:port network-credentials)}]]
                         (concat tcp-env-vars env-vars))

                              ;; Caso padrão
                       :else env-vars)

        secret (clj->js
                (merge
                 (helpers/config->json all-env-vars "envvar:")
                 (when (seq config-files)
                   (helpers/config->json config-files "filesystem:"))))

        guardrails-processed (mapv #(get % "value") guardrails)

        ;; Garante valores padrão para os access modes
        default-access-modes {:runbooks true :native true :web true}
        effective-access-modes (merge default-access-modes access-modes)

        command-string (get-in db [:connection-setup :command])
        payload {:type api-type
                 :subtype connection-subtype
                 :name connection-name
                 :agent_id agent-id
                 :tags (when (seq tags-array)
                         (mapv #(get % "value") tags-array))
                 :secret secret
                 :command (if (= api-type "database")
                            []
                            (when-not (empty? command-string)
                              (or (re-seq #"'.*?'|\".*?\"|\S+|\t" command-string) [])))
                 :guardrail_rules guardrails-processed
                 :jira_issue_template_id jira-template-id
                 :access_schema (or (when (= api-type "database")
                                      (if (:database-schema config) "enabled" "disabled"))
                                    "disabled")
                 :access_mode_runbooks (if (:runbooks effective-access-modes) "enabled" "disabled")
                 :access_mode_exec (if (:web effective-access-modes) "enabled" "disabled")
                 :access_mode_connect (if (:native effective-access-modes) "enabled" "disabled")
                 :redact_enabled (:data-masking config false)
                 :redact_types (when (seq data-masking-types)
                                 (mapv #(get % "value") data-masking-types))
                 :reviewers (when (and (:review config) (seq review-groups))
                              (mapv #(get % "value") review-groups))}]

    payload))

;; Update an existing connection
(defn decode-base64 [s]
  (-> s
      js/atob
      js/decodeURIComponent))

(defn process-connection-secret
  "Processa os valores secret da conexão de base64 para string"
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
  "Processa os valores secret da conexão de base64 para string"
  [secret secret-type]
  (let [secret-start-name (if (= secret-type "envvar")
                            "envvar:"
                            "filesystem:")]
    (reduce-kv (fn [acc k v]
                 (if (str/starts-with? (name k) secret-start-name)
                   (let [clean-key (-> (name k)
                                       (str/replace secret-start-name "")
                                       str/lower-case)]
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
  "Extrai HOST e PORT dos secrets para network credentials"
  [credentials]
  {:host (get credentials "host")
   :port (get credentials "port")})

(defn process-connection-for-update
  "Processa uma conexão existente para o formato usado no formulário de atualização"
  [connection guardrails-list jira-templates-list]
  (let [credentials (process-connection-secret (:secret connection) "envvar")
        network-credentials (when (and (= (:type connection) "application")
                                       (= (:subtype connection) "tcp"))
                              (extract-network-credentials credentials))]
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
     :config {:review (seq (:reviewers connection))
              :review-groups (mapv #(hash-map "value" % "label" %) (:reviewers connection))
              :data-masking (:redact_enabled connection)
              :data-masking-types (mapv #(hash-map "value" % "label" %) (:redact_types connection))
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
                                  "")}
     :tags (when (seq (:tags connection))
             (mapv #(into {} {"value" % "label" %}) (:tags connection)))}))
