(ns webapp.resources.setup.events.process-form
  (:require
   [clojure.string :as str]
   [webapp.resources.helpers :as helpers]))

(defn process-http-headers
  "Process HTTP headers by adding HEADER_ prefix to each key"
  [headers]
  (mapv (fn [{:keys [key value]}]
          {:key (str "HEADER_" key)
           :value value})
        headers))

(defn extract-value
  "Extract value from map or string, applying prefix if present."
  [v connection-method field-key]
  (let [value (if (map? v) (:value v "") (str v))
        prefix (if (map? v) (:prefix v "") "")
        is-aws-iam-role? (= connection-method "aws-iam-role")
        field-key-lower (str/lower-case (name field-key))
        is-user-or-pass? (or (= field-key-lower "user") (= field-key-lower "pass"))
        final-value (cond
                      (and is-aws-iam-role? is-user-or-pass?)
                      (str "_aws_iam_rds:" value)
                      (not (str/blank? prefix))
                      (str prefix value)
                      :else
                      value)]
    final-value))

(defn process-role-secret
  "Process role credentials into secret format with base64 encoding"
  [role]
  (let [subtype (:subtype role)
        connection-method (:connection-method role)
        credentials (:credentials role)
        metadata-credentials (:metadata-credentials role)
        env-vars (or (:environment-variables role) [])
        config-files (or (:configuration-files role) [])

        credential-env-vars (mapv (fn [[k v]]
                                    {:key (name k)
                                     :value (extract-value v connection-method k)})
                                  (seq credentials))

        metadata-credential-env-vars (mapv (fn [[k v]]
                                             {:key (name k)
                                              :value (extract-value v connection-method k)})
                                           (seq metadata-credentials))

        ;; Combine all credentials
        all-credential-env-vars (concat credential-env-vars metadata-credential-env-vars)

        ;; Special handling for httpproxy headers
        all-env-vars (if (= subtype "httpproxy")
                       (let [headers (:environment-variables role [])
                             processed-headers (process-http-headers headers)]
                         (concat all-credential-env-vars processed-headers))
                       (concat all-credential-env-vars env-vars))

        processed-config-files (mapv (fn [file]
                                       {:key (:key file)
                                        :value (extract-value (:value file) connection-method (:key file))})
                                     config-files)

        envvar-result (helpers/config->json all-env-vars "envvar:")
        filesystem-result (when (seq processed-config-files)
                            (helpers/config->json processed-config-files "filesystem:"))]

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
        env-current-value (:env-current-value role)
        has-pending-env? (and (not (str/blank? env-current-key))
                              (not (str/blank? env-current-value)))

        ;; Get current config file values
        config-current-name (:config-current-name role)
        config-current-content (:config-current-content role)
        has-pending-config? (and (not (str/blank? config-current-name))
                                 (not (str/blank? config-current-content)))

        ;; Add pending env var if exists
        updated-env-vars (if has-pending-env?
                           (conj (or (:environment-variables role) [])
                                 {:key env-current-key :value env-current-value})
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

