(ns webapp.resources.views.setup.events.process-form
  (:require
   [webapp.resources.helpers :as helpers]))

(defn process-http-headers
  "Process HTTP headers by adding HEADER_ prefix to each key"
  [headers]
  (mapv (fn [{:keys [key value]}]
          {:key (str "HEADER_" key)
           :value value})
        headers))

(defn process-role-secret
  "Process role credentials into secret format with base64 encoding"
  [role]
  (let [subtype (:subtype role)
        credentials (:credentials role)
        metadata-credentials (:metadata-credentials role)
        env-vars (:environment-variables role [])
        config-files (:configuration-files role [])

        ;; Convert credentials to env-var format
        credential-env-vars (mapv (fn [[k v]]
                                    {:key (name k)
                                     :value v})
                                  (seq credentials))

        ;; Convert metadata-credentials to env-var format (for custom resources)
        metadata-credential-env-vars (mapv (fn [[k v]]
                                             {:key (name k)
                                              :value v})
                                           (seq metadata-credentials))

        ;; Combine all credentials
        all-credential-env-vars (concat credential-env-vars metadata-credential-env-vars)

        ;; Special handling for httpproxy headers
        all-env-vars (if (= subtype "httpproxy")
                       (let [headers (:environment-variables role [])
                             processed-headers (process-http-headers headers)]
                         (concat all-credential-env-vars processed-headers))
                       (concat all-credential-env-vars env-vars))]

    (clj->js
     (merge
      (helpers/config->json all-env-vars "envvar:")
      (when (seq config-files)
        (helpers/config->json config-files "filesystem:"))))))

(defn process-role
  "Process a single role into the format expected by the API"
  [role agent-id]
  (let [type (:type role)
        subtype (:subtype role)
        secret (process-role-secret role)

        ;; Build command array for custom types
        command (if (= type "custom")
                  (or (:command role) [])
                  [])]

    {:name (:name role)
     :type type
     :subtype subtype
     :agent_id agent-id
     :secret secret
     :command command
     :access_mode_runbooks "enabled"
     :access_mode_exec "enabled"
     :access_mode_connect "enabled"
     :access_schema "enabled"
     :redact_enabled false
     :redact_types []
     :reviewers []}))

(defn process-payload
  "Process the entire resource setup form into API payload"
  [db]
  (let [resource-name (get-in db [:resource-setup :name])
        resource-subtype (get-in db [:resource-setup :subtype])
        agent-id (get-in db [:resource-setup :agent-id])
        roles (get-in db [:resource-setup :roles] [])

        ;; Process all roles
        processed-roles (mapv #(process-role % agent-id) roles)]

    {:name resource-name
     :type resource-subtype  ;; Backend expects subtype as "type" (e.g., "postgres")
     :agent_id agent-id
     :env_vars {}
     :roles processed-roles}))

