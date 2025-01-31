(ns webapp.connections.views.setup.events.process-form
  (:require [webapp.connections.helpers :as helpers]))

(defn get-api-connection-type [ui-type]
  (case ui-type
    "network" "application"
    "server" "custom"
    "database" "database"))

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

        ;; Garante valores padrão para os access modes
        default-access-modes {:runbooks true :native true :web true}
        effective-access-modes (merge default-access-modes access-modes)

        command-string (get-in db [:connection-setup :command])
        payload {:type api-type
                 :subtype connection-subtype
                 :name connection-name
                 :agent_id agent-id
                 :tags (when (seq tags) tags)
                 :secret secret
                 :command (if (= api-type "database")
                            []
                            (when-not (empty? command-string)
                              (or (re-seq #"'.*?'|\".*?\"|\S+|\t" command-string) [])))
                 :access_schema (when (= api-type "database")
                                  (if (:database-schema config) "enabled" "disabled"))
                 :access_mode_runbooks (if (:runbooks effective-access-modes) "enabled" "disabled")
                 :access_mode_exec (if (:web effective-access-modes) "enabled" "disabled")
                 :access_mode_connect (if (:native effective-access-modes) "enabled" "disabled")
                 :redact_enabled (:data-masking config false)
                 :redact_types (when (seq data-masking-types)
                                 (mapv #(get % "value") data-masking-types))
                 :reviewers (when (and (:review config) (seq review-groups))
                              (mapv #(get % "value") review-groups))}]

    ;; Remove o access_schema se não for database
    (let [final-payload (if-not (= api-type "database")
                          (dissoc payload :access_schema)
                          payload)]
      ;; Debug output
      (js/console.log "Submitting connection payload:" (clj->js final-payload))

      final-payload)))
