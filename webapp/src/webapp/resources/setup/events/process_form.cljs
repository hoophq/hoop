(ns webapp.resources.setup.events.process-form
  (:require
   [clojure.string :as str]
   [webapp.resources.helpers :as helpers]
   [webapp.resources.constants :refer [http-proxy-subtypes]]))

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

(defn process-role-secret
  "Process role credentials into secret format with base64 encoding"
  [role]
  (let [subtype (:subtype role)
        connection-method (:connection-method role)
        secrets-provider (or (:secrets-manager-provider role) "vault-kv1")
        credentials (:credentials role)
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
        all-env-vars (if (http-proxy-subtypes subtype)
                       (let [headers (:environment-variables role [])
                             processed-headers (mapv (fn [{:keys [key value]}]
                                                       {:key (str "HEADER_" key)
                                                        :value (extract-value value connection-method (keyword key) secrets-provider)})
                                                     headers)]
                         (concat all-credential-env-vars processed-headers))
                       (concat all-credential-env-vars processed-env-vars))

        envvar-result (helpers/config->json all-env-vars "envvar:")
        filesystem-result (when (seq config-files)
                            (helpers/config->json config-files "filesystem:"))]

    (clj->js
     (merge envvar-result filesystem-result))))

(defn process-role
  "Process a single role into the format expected by the API"
  [role agent-id & [command-role]]
  (let [type (:type role)
        subtype (:subtype role)
        secret (process-role-secret role)
        command-role (if command-role
                       command-role
                       (:command role))

        ;; Build command array for custom types
        ;; command-args is stored as array of {"value": "...", "label": "..."}
        ;; Extract just the values

        command-args (:command-args role [])
        command (if (and (= type "custom")
                         (= subtype "linux-vm"))
                  (mapv #(get % "value") command-args)
                  (or command-role []))]

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

(defn process-payload
  "Process the entire resource setup form into API payload"
  [db]
  (let [resource-name (get-in db [:resource-setup :name])
        resource-type (get-in db [:resource-setup :type])
        resource-subtype (get-in db [:resource-setup :subtype])
        agent-id (get-in db [:resource-setup :agent-id])
        raw-roles (get-in db [:resource-setup :roles] [])

        ;; Finalize roles by adding any uncommitted current values
        roles (mapv finalize-role-current-values raw-roles)

        ;; Process all roles
        processed-roles (mapv #(process-role % agent-id) roles)]

    {:name resource-name
     :type resource-type
     :subtype resource-subtype
     :agent_id agent-id
     :env_vars {}
     :roles processed-roles}))

