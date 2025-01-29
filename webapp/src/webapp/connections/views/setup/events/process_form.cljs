(ns webapp.connections.views.setup.events.process-form
  (:require [webapp.connections.helpers :as helpers]))

(defn process-database-payload [db]
  (let [connection-type (get-in db [:connection-setup :database-type])
        connection-name (get-in db [:connection-setup :name])
        agent-id (get-in db [:connection-setup :agent-id])
        tags (get-in db [:connection-setup :tags])
        config (get-in db [:connection-setup :config])
        env-vars (get-in db [:connection-setup :environment-variables] [])
        config-files (get-in db [:connection-setup :configuration-files] [])
        review-groups (get-in config [:review-groups])
        data-masking-types (get-in config [:data-masking-types])
        database-credentials (get-in db [:connection-setup :database-credentials])
        access-modes (get-in config [:access-modes])

        ;; Processa as credenciais do banco de dados para o formato de environment variables
        credentials-as-env-vars
        (mapv (fn [[k v]]
                {:key (name k)
                 :value v})
              (seq database-credentials))

        ;; Combina as credenciais do banco com outras variáveis de ambiente
        all-env-vars (concat credentials-as-env-vars env-vars)

        secret (clj->js
                (merge
                 (helpers/config->json all-env-vars "envvar:")
                 (when (seq config-files)
                   (helpers/config->json config-files "filesystem:"))))

        default-access-modes {:runbooks true :native true :web true}
        effective-access-modes (merge default-access-modes access-modes)

        payload {:type "database"
                 :subtype connection-type
                 :name connection-name
                 :agent_id agent-id
                 :tags (when (seq tags)
                         (mapv #(get % "value") tags))
                 :secret secret
                 :command []
                 :access_schema (if (:database-schema config) "enabled" "disabled")
                 :access_mode_runbooks (if (:runbooks effective-access-modes) "enabled" "disabled")
                 :access_mode_exec (if (:web effective-access-modes) "enabled" "disabled")
                 :access_mode_connect (if (:native effective-access-modes) "enabled" "disabled")
                 :redact_enabled (:data-masking config false)
                 :redact_types (when (seq data-masking-types)
                                 (mapv #(get % "value") data-masking-types))
                 :reviewers (when (and (:review config) (seq review-groups))
                              (mapv #(get % "value") review-groups))}]

    ;; Debug output
    (js/console.log "Submitting database connection with payload:"
                    (clj->js payload))

    payload))

(defn process-payload [db]
  (let [connection-type (get-in db [:connection-setup :type])]
    (case connection-type
      "database" (process-database-payload db)
      ;; Adicionar outros tipos conforme necessário
      nil)))
