(ns webapp.onboarding.events.aws-connect-events
  (:require [re-frame.core :as rf]))

;; Initialize AWS Connect state
(rf/reg-event-fx
 :aws-connect/initialize-state
 (fn [db _]
   {:db (assoc db :aws-connect {:current-step :credentials
                                :status nil
                                :loading {:active? false
                                          :message nil}
                                :error nil
                                :credentials nil
                                :resources {:data nil
                                            :selected nil
                                            :errors nil
                                            :status nil
                                            :connection-names {}}
                                :agents {:data nil
                                         :assignments nil}})
    ;; Carregar agentes reais
    :dispatch [:aws-connect/fetch-agents]}))

;; Setup type event
(rf/reg-event-db
 :connection-setup/set-type
 (fn [db [_ type]]
   (-> db
       (assoc-in [:connection-setup :type] type)
       (assoc-in [:aws-connect :current-step] :credentials))))

;; Navigation events
(rf/reg-event-db
 :aws-connect/set-current-step
 (fn [db [_ step]]
   (assoc-in db [:aws-connect :current-step] step)))

;; Credentials events - removed IAM Role, focusing only on IAM User Credentials
(rf/reg-event-db
 :aws-connect/set-iam-user-credentials
 (fn [db [_ field value]]
   (assoc-in db [:aws-connect :credentials :iam-user field] value)))

;; Resources events
(rf/reg-event-db
 :aws-connect/set-selected-resources
 (fn [db [_ selected]]
   (assoc-in db [:aws-connect :resources :selected] selected)))

;; Agent assignment events
(rf/reg-event-db
 :aws-connect/set-agent-assignment
 (fn [db [_ resource-id agent-id]]
   (assoc-in db [:aws-connect :agents :assignments resource-id] agent-id)))

;; Validation events - Fluxo de 3 etapas
;; 1. Adicionar credenciais à base de dados
;; 2. Validar se as credenciais são válidas
;; 3. Buscar recursos RDS

;; Etapa 1 - Iniciar o fluxo de validação de credenciais
(rf/reg-event-fx
 :aws-connect/validate-credentials
 (fn [{:keys [db]} _]
   (let [credentials (get-in db [:aws-connect :credentials])
         aws-credentials {:access_key_id (get-in credentials [:iam-user :access-key-id])
                          :secret_access_key (get-in credentials [:iam-user :secret-access-key])
                          :region (get-in credentials [:iam-user :region])
                          :session_token (get-in credentials [:iam-user :session-token])}]
     ;; 1.a. Etapa: Salvar as credenciais no gateway
     {:db (-> db
              (assoc-in [:aws-connect :status] :validating)
              (assoc-in [:aws-connect :loading :active?] true)
              (assoc-in [:aws-connect :loading :message] "Saving AWS credentials..."))
      :dispatch [:aws-connect/save-credentials aws-credentials]})))

;; 1.a. Etapa: Salvar credenciais AWS
(rf/reg-event-fx
 :aws-connect/save-credentials
 (fn [{:keys [db]} [_ aws-credentials]]
   {:dispatch [:fetch
               {:method "PUT"
                :uri "/integrations/aws/iam/accesskeys"
                :body aws-credentials
                :on-success #(rf/dispatch [:aws-connect/save-credentials-success %])
                :on-failure #(rf/dispatch [:aws-connect/save-credentials-failure %])}]}))

(rf/reg-event-fx
 :aws-connect/save-credentials-success
 (fn [{:keys [db]} _]
   ;; 1.b. Segunda etapa: Verificar se as credenciais são válidas
   {:db (assoc-in db [:aws-connect :loading :message] "Verifying AWS credentials...")
    :dispatch [:aws-connect/verify-credentials]}))

(rf/reg-event-fx
 :aws-connect/save-credentials-failure
 (fn [{:keys [db]} [_ response]]
   {:db (-> db
            (assoc-in [:aws-connect :status] :credentials-invalid)
            (assoc-in [:aws-connect :loading :active?] false)
            (assoc-in [:aws-connect :loading :message] nil)
            (assoc-in [:aws-connect :error] (get-in response [:response :message] "Failed to save AWS credentials")))
    :dispatch [:show-snackbar {:level :error
                               :text "Failed to save AWS credentials. Please check your inputs and try again."}]}))

;; 1.b. Etapa: Verificar credenciais AWS
(rf/reg-event-fx
 :aws-connect/verify-credentials
 (fn [{:keys [db]} _]
   {:dispatch [:fetch
               {:method "POST"
                :uri "/integrations/aws/iam/verify"
                :on-success #(rf/dispatch [:aws-connect/verify-credentials-success %])
                :on-failure #(rf/dispatch [:aws-connect/verify-credentials-failure %])}]}))

(rf/reg-event-fx
 :aws-connect/verify-credentials-success
 (fn [{:keys [db]} [_ response]]
   (let [status (get response :status)]
     (if (= status "allowed")
       ;; 1.c. Terceira etapa: Buscar recursos RDS
       {:db (assoc-in db [:aws-connect :loading :message] "Retrieving AWS access information in your environment...")
        :dispatch [:aws-connect/fetch-rds-instances]}
       {:db (-> db
                (assoc-in [:aws-connect :status] :credentials-invalid)
                (assoc-in [:aws-connect :loading :active?] false)
                (assoc-in [:aws-connect :loading :message] nil)
                (assoc-in [:aws-connect :error] "Insufficient permissions to access AWS resources"))
        :dispatch [:show-snackbar {:level :error
                                   :text "Your AWS credentials don't have sufficient permissions."}]}))))

(rf/reg-event-fx
 :aws-connect/verify-credentials-failure
 (fn [{:keys [db]} [_ response]]
   {:db (-> db
            (assoc-in [:aws-connect :status] :credentials-invalid)
            (assoc-in [:aws-connect :loading :active?] false)
            (assoc-in [:aws-connect :loading :message] nil)
            (assoc-in [:aws-connect :error] (get-in response [:response :message] "Failed to verify AWS credentials")))
    :dispatch [:show-snackbar {:level :error
                               :text "Failed to verify AWS credentials. Please check your inputs and try again."}]}))

;; 1.c. Etapa: Buscar instâncias de RDS
(rf/reg-event-fx
 :aws-connect/fetch-rds-instances
 (fn [{:keys [db]} _]
   {:dispatch [:fetch
               {:method "POST"
                :uri "/integrations/aws/rds/describe-db-instances"
                :body {}
                :on-success #(rf/dispatch [:aws-connect/fetch-rds-instances-success %])
                :on-failure #(rf/dispatch [:aws-connect/fetch-rds-instances-failure %])}]}))

(rf/reg-event-fx
 :aws-connect/fetch-rds-instances-success
 (fn [{:keys [db]} [_ response]]
   (let [rds-instances (get response :items [])
         formatted-resources (mapv (fn [instance]
                                     {:id (:arn instance)
                                      :name (:name instance)
                                      :subnet-cidr ""  ;; Não temos esse dado diretamente da API
                                      :vpc-id (:vpc_id instance)
                                      :status (:status instance)
                                      :security-group-enabled? false
                                      :engine (:engine instance)
                                      :account-id (:account_id instance)})
                                   rds-instances)]
     ;; Transformar os dados para o formato esperado pelo app
     {:db (-> db
              (assoc-in [:aws-connect :status] :credentials-valid)
              (assoc-in [:aws-connect :loading :active?] false)
              (assoc-in [:aws-connect :loading :message] nil)
              (assoc-in [:aws-connect :resources :data] formatted-resources)
              (assoc-in [:aws-connect :resources :status] :loaded)
              (assoc-in [:aws-connect :resources :api-error] nil)) ;; Limpar qualquer erro anterior
      :dispatch [:aws-connect/set-current-step :resources]})))

(rf/reg-event-fx
 :aws-connect/fetch-rds-instances-failure
 (fn [{:keys [db]} [_ response]]
   (let [;; Extract error message from the API response structure
         ;; The API returns errors in { "message": "error text" } format
         raw-response (get-in response [:response] {})
         error-message (or (get raw-response :message)
                           (get-in raw-response [:body :message])
                           (get-in raw-response [:data :message])
                           "Failed to fetch RDS instances")
         error-details (or (get-in raw-response [:errors])
                           (get-in raw-response [:body :errors])
                           (get-in raw-response [:data :errors])
                           [])
         ;; Store the complete raw response for debugging
         api-error {:message error-message
                    :details error-details
                    :status (get response :status 500)
                    :raw-response raw-response}]
     {:db (-> db
              (assoc-in [:aws-connect :status] nil)
              (assoc-in [:aws-connect :loading :active?] false)
              (assoc-in [:aws-connect :loading :message] nil)
              (assoc-in [:aws-connect :error] error-message)
              (assoc-in [:aws-connect :resources :status] :error)
              (assoc-in [:aws-connect :resources :data] [])
              (assoc-in [:aws-connect :resources :api-error] api-error))
      :dispatch [:show-snackbar {:level :error
                                 :text "Failed to retrieve database instances from AWS. Please check your credentials and try again."}]})))

;; Create connections event
(rf/reg-event-fx
 :aws-connect/create-connections
 (fn [{:keys [db]} _]
   (let [selected-resources (get-in db [:aws-connect :resources :selected])
         resources (get-in db [:aws-connect :resources :data])
         agent-assignments (get-in db [:aws-connect :agents :assignments])
         connection-names (get-in db [:aws-connect :resources :connection-names])

         ;; Filtrar apenas os recursos selecionados
         selected-resource-data (filter #(contains? selected-resources (:id %)) resources)

         ;; Initialize status map with all connections as "pending"
         initial-status-map (reduce (fn [acc resource]
                                      (assoc acc (:id resource)
                                             {:status "pending"
                                              :name (get connection-names (:id resource)
                                                         (str (:name resource) "-" (:account-id resource)))
                                              :resource resource
                                              :error nil}))
                                    {}
                                    selected-resource-data)]

     {:db (-> db
              (assoc-in [:aws-connect :status] :creating)
              (assoc-in [:aws-connect :loading :active?] false) ;; We'll handle loading in our own UI
              (assoc-in [:aws-connect :creation-status]
                        {:all-completed? false
                         :connections initial-status-map})
              (assoc-in [:aws-connect :current-step] :creation-status)) ;; New step for status view
      :dispatch [:aws-connect/process-resources selected-resource-data agent-assignments connection-names]})))

(rf/reg-event-fx
 :aws-connect/process-resources
 (fn [{:keys [db]} [_ resources agent-assignments connection-names]]
   (let [total-resources (count resources)
         dispatch-requests (for [resource resources
                                 :let [agent-id (get agent-assignments (:id resource) "default")
                                       resource-arn (:id resource)
                                       connection-prefix (or (get connection-names (:id resource))
                                                             (str (:name resource) "-" (:account-id resource)))]]
                             [:fetch
                              {:method "POST"
                               :uri "/dbroles/jobs"
                               :body {:agent_id agent-id
                                      :aws {:instance_arn resource-arn}
                                      :connection_prefix_name connection-prefix}
                               :on-success #(rf/dispatch [:aws-connect/connection-created-success % resource])
                               :on-failure #(rf/dispatch [:aws-connect/connection-created-failure % resource])}])]
     ;; Initialize creation status tracking
     {:db (assoc-in db [:aws-connect :resources :total-to-process] total-resources)
      :dispatch-n dispatch-requests})))

;; Update to track individual connection statuses
(rf/reg-event-fx
 :aws-connect/connection-created-success
 (fn [{:keys [db]} [_ response resource]]
   (let [resource-id (:id resource)
         connection-name (get-in db [:aws-connect :creation-status :connections resource-id :name])
         updated-db (-> db
                       ;; Update this specific connection status
                        (assoc-in [:aws-connect :creation-status :connections resource-id :status] "success")
                       ;; Remove any error if previously set
                        (assoc-in [:aws-connect :creation-status :connections resource-id :error] nil))

         ;; Check if all connections are now completed (success or failure)
         all-connections (get-in updated-db [:aws-connect :creation-status :connections])
         all-completed? (every? #(contains? #{"success" "failure"} (:status %))
                                (vals all-connections))]

     {:db (-> updated-db
              ;; Update all-completed flag if everything is done
              (assoc-in [:aws-connect :creation-status :all-completed?] all-completed?))
      :dispatch [:show-snackbar {:level :success
                                 :text (str "Connection " connection-name " created successfully!")}]})))

(rf/reg-event-fx
 :aws-connect/connection-created-failure
 (fn [{:keys [db]} [_ response resource]]
   (println response)
   (let [resource-id (:id resource)
         connection-name (get-in db [:aws-connect :creation-status :connections resource-id :name])
         error-message (or response
                           "Failed to create connection")

         updated-db (-> db
                       ;; Update this specific connection status
                        (assoc-in [:aws-connect :creation-status :connections resource-id :status] "failure")
                       ;; Store the error message
                        (assoc-in [:aws-connect :creation-status :connections resource-id :error] error-message))

         ;; Check if all connections are now completed (success or failure)
         all-connections (get-in updated-db [:aws-connect :creation-status :connections])
         all-completed? (every? #(contains? #{"success" "failure"} (:status %))
                                (vals all-connections))]

     {:db (-> updated-db
              ;; Update all-completed flag if everything is done
              (assoc-in [:aws-connect :creation-status :all-completed?] all-completed?))
      :dispatch [:show-snackbar {:level :error
                                 :text (str "Failed to create connection " connection-name)}]})))

;; Subscriptions
(rf/reg-sub
 :aws-connect/current-step
 (fn [db _]
   (get-in db [:aws-connect :current-step])))

(rf/reg-sub
 :aws-connect/status
 (fn [db _]
   (get-in db [:aws-connect :status])))

(rf/reg-sub
 :aws-connect/error
 (fn [db _]
   (get-in db [:aws-connect :error])))

(rf/reg-sub
 :aws-connect/credentials
 (fn [db _]
   (get-in db [:aws-connect :credentials])))

(rf/reg-sub
 :aws-connect/resources
 (fn [db _]
   (get-in db [:aws-connect :resources :data])))

(rf/reg-sub
 :aws-connect/selected-resources
 (fn [db _]
   (get-in db [:aws-connect :resources :selected])))

(rf/reg-sub
 :aws-connect/resources-errors
 (fn [db _]
   (get-in db [:aws-connect :resources :errors])))

(rf/reg-sub
 :aws-connect/agents
 (fn [db _]
   (get-in db [:aws-connect :agents :data])))

(rf/reg-sub
 :aws-connect/agent-assignments
 (fn [db _]
   (get-in db [:aws-connect :agents :assignments])))

;; Subscription para o estado de loading
(rf/reg-sub
 :aws-connect/loading
 (fn [db _]
   (get-in db [:aws-connect :loading])))

;; Carrega os agentes da API
(rf/reg-event-fx
 :aws-connect/fetch-agents
 (fn [{:keys [db]} _]
   {:dispatch [:fetch
               {:method "GET"
                :uri "/agents"
                :on-success #(rf/dispatch [:aws-connect/fetch-agents-success %])
                :on-failure #(rf/dispatch [:aws-connect/fetch-agents-failure %])}]}))

(rf/reg-event-db
 :aws-connect/fetch-agents-success
 (fn [db [_ agents]]
   (assoc-in db [:aws-connect :agents :data] agents)))

(rf/reg-event-fx
 :aws-connect/fetch-agents-failure
 (fn [{:keys [db]} [_ response]]
   {:db (assoc-in db [:aws-connect :agents :data] [])
    :dispatch [:show-snackbar {:level :error
                               :text "Failed to load agents. Using default options."}]}))

;; Subscription para acessar erros de API de recursos
(rf/reg-sub
 :aws-connect/resources-api-error
 (fn [db _]
   (get-in db [:aws-connect :resources :api-error])))

;; Subscription para verificar o status dos recursos
(rf/reg-sub
 :aws-connect/resources-status
 (fn [db _]
   (get-in db [:aws-connect :resources :status])))

(rf/reg-sub
 :aws-connect/connection-names
 (fn [db _]
   (get-in db [:aws-connect :resources :connection-names])))

(rf/reg-event-db
 :aws-connect/set-connection-name
 (fn [db [_ resource-id connection-name]]
   (assoc-in db [:aws-connect :resources :connection-names resource-id] connection-name)))

;; Add subscription for creation status
(rf/reg-sub
 :aws-connect/creation-status
 (fn [db _]
   (get-in db [:aws-connect :creation-status])))
