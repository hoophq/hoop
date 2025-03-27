(ns webapp.onboarding.events.aws-connect-events
  (:require [re-frame.core :as rf]))

(rf/reg-event-fx
 :aws-connect/initialize-state
 (fn [{:keys [db]} _]
   {:db (assoc db :aws-connect {:current-step :credentials
                                :status nil
                                :loading {:active? false
                                          :message nil}
                                :error nil
                                :credentials nil
                                :accounts {:data nil
                                           :status nil
                                           :selected #{}
                                           :api-error nil}
                                :resources {:data nil
                                            :selected nil
                                            :errors nil
                                            :status nil
                                            :connection-names {}
                                            :security-groups {}
                                            :ports {}}
                                :agents {:data nil
                                         :assignments nil}
                                :create-connection true})
    :dispatch [:aws-connect/fetch-agents]}))

(rf/reg-event-db
 :connection-setup/set-type
 (fn [db [_ type]]
   (-> db
       (assoc-in [:connection-setup :type] type)
       (assoc-in [:aws-connect :current-step] :credentials))))

(rf/reg-event-db
 :aws-connect/set-current-step
 (fn [db [_ step]]
   (assoc-in db [:aws-connect :current-step] step)))

(rf/reg-event-db
 :aws-connect/set-iam-user-credentials
 (fn [db [_ field value]]
   (assoc-in db [:aws-connect :credentials :iam-user field] value)))

(rf/reg-event-db
 :aws-connect/set-selected-resources
 (fn [db [_ selected]]
   (assoc-in db [:aws-connect :resources :selected] selected)))

(rf/reg-event-db
 :aws-connect/set-agent-assignment
 (fn [db [_ resource-id agent-id]]
   (assoc-in db [:aws-connect :agents :assignments resource-id] agent-id)))

(rf/reg-event-fx
 :aws-connect/validate-credentials
 (fn [{:keys [db]} _]
   (let [credentials (get-in db [:aws-connect :credentials])
         aws-credentials {:access_key_id (get-in credentials [:iam-user :access-key-id])
                          :secret_access_key (get-in credentials [:iam-user :secret-access-key])
                          :region (get-in credentials [:iam-user :region])
                          :session_token (get-in credentials [:iam-user :session-token])}]
     {:db (-> db
              (assoc-in [:aws-connect :status] :validating)
              (assoc-in [:aws-connect :loading :active?] true)
              (assoc-in [:aws-connect :loading :message] "Saving AWS credentials..."))
      :dispatch [:aws-connect/save-credentials aws-credentials]})))

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
   {:db (assoc-in db [:aws-connect :loading :message] "Verifying AWS credentials...")
    :dispatch [:aws-connect/verify-credentials]}))

(rf/reg-event-fx
 :aws-connect/save-credentials-failure
 (fn [{:keys [db]} [_ response]]
   {:db (-> db
            (assoc-in [:aws-connect :status] :credentials-invalid)
            (assoc-in [:aws-connect :loading :active?] false)
            (assoc-in [:aws-connect :loading :message] nil)
            (assoc-in [:aws-connect :error] (or response "Failed to save AWS credentials")))
    :dispatch [:show-snackbar {:level :error
                               :text "Failed to save AWS credentials. Please check your inputs and try again."}]}))

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
       {:db (assoc-in db [:aws-connect :loading :message] "Retrieving AWS organization accounts...")
        :dispatch [:aws-connect/fetch-accounts]}
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
   (println response)
   {:db (-> db
            (assoc-in [:aws-connect :status] :credentials-invalid)
            (assoc-in [:aws-connect :loading :active?] false)
            (assoc-in [:aws-connect :loading :message] nil)
            (assoc-in [:aws-connect :error] (or response "Failed to verify AWS credentials")))
    :dispatch [:show-snackbar {:level :error
                               :text "Failed to verify AWS credentials. Please check your inputs and try again."}]}))

(rf/reg-event-fx
 :aws-connect/fetch-rds-instances
 (fn [{:keys [db]} _]
   (let [selected-accounts (get-in db [:aws-connect :accounts :selected])]
     {:db (-> db
              (assoc-in [:aws-connect :loading :active?] true)
              (assoc-in [:aws-connect :loading :message] "Retrieving AWS resources in your environment..."))
      :dispatch [:fetch
                 {:method "POST"
                  :uri "/integrations/aws/rds/describe-db-instances"
                  :body {:account_ids (vec selected-accounts)}
                  :on-success #(rf/dispatch [:aws-connect/fetch-rds-instances-success %])
                  :on-failure #(rf/dispatch [:aws-connect/fetch-rds-instances-failure %])}]})))

(rf/reg-event-fx
 :aws-connect/fetch-rds-instances-success
 (fn [{:keys [db]} [_ response]]
   (let [rds-instances (get response :items [])
         accounts (get-in db [:aws-connect :accounts :data] [])

         ;; Group resources by account_id for hierarchical structure
         resources-by-account (reduce (fn [acc instance]
                                        (let [account-id (:account_id instance)]
                                          (update acc account-id (fnil conj []) instance)))
                                      {}
                                      rds-instances)

         ;; Format resources
         formatted-resources (mapv (fn [account]
                                     (let [account-id (:account_id account)
                                           account-resources (get resources-by-account account-id [])

                                           ;; Format child resources
                                           formatted-children (mapv (fn [instance]
                                                                      {:id (:arn instance)
                                                                       :name (:name instance)
                                                                       :subnet-cidr ""
                                                                       :vpc-id (:vpc_id instance)
                                                                       :status (:status instance)
                                                                       :security-group-enabled? false
                                                                       :engine (:engine instance)
                                                                       :account-id account-id})
                                                                    account-resources)]

                                       ;; Format parent account
                                       {:id account-id
                                        :name (:name account)
                                        :status (:status account)
                                        :email (:email account)
                                        :account-type "AWS Account"
                                        :children formatted-children}))
                                   accounts)]

     {:db (-> db
              (assoc-in [:aws-connect :status] :credentials-valid)
              (assoc-in [:aws-connect :loading :active?] false)
              (assoc-in [:aws-connect :loading :message] nil)
              (assoc-in [:aws-connect :resources :data] formatted-resources)
              (assoc-in [:aws-connect :resources :status] :loaded)
              (assoc-in [:aws-connect :resources :api-error] nil))
      :dispatch [:aws-connect/set-current-step :resources]})))

(rf/reg-event-fx
 :aws-connect/fetch-rds-instances-failure
 (fn [{:keys [db]} [_ response]]
   (let [error-message (or response "Failed to fetch RDS instances")
         api-error {:message error-message
                    :status (get response :status 500)}]
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

(rf/reg-event-fx
 :aws-connect/create-connections
 (fn [{:keys [db]} _]
   (let [selected-resources (get-in db [:aws-connect :resources :selected])
         resources (get-in db [:aws-connect :resources :data])
         agent-assignments (get-in db [:aws-connect :agents :assignments])
         connection-names (get-in db [:aws-connect :resources :connection-names])
         security-groups (get-in db [:aws-connect :resources :security-groups])
         ports (get-in db [:aws-connect :resources :ports])

         ;; Flatten the hierarchy to get all selected resource data
         selected-resource-data (reduce (fn [acc account]
                                          ;; Get all selected children resources
                                          (let [account-id (:id account)
                                                children (:children account)
                                                selected-children (filter #(contains? selected-resources (:id %)) children)]

                                            ;; Add selected children resources to accumulator
                                            (concat acc selected-children)))
                                        []
                                        resources)

         ;; Create initial status map
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
              (assoc-in [:aws-connect :loading :active?] false)
              (assoc-in [:aws-connect :creation-status]
                        {:all-completed? false
                         :connections initial-status-map})
              (assoc-in [:aws-connect :current-step] :creation-status))
      :dispatch [:aws-connect/process-resources selected-resource-data agent-assignments connection-names security-groups ports]})))

(rf/reg-event-fx
 :aws-connect/process-resources
 (fn [{:keys [db]} [_ resources agent-assignments connection-names security-groups ports]]
   (let [total-resources (count resources)
         default-ports {"mysql" 3306
                        "postgres" 5432
                        "postgresql" 5432
                        "sqlserver-ex" 1433
                        "sqlserver" 1433}
         create-connection (get-in db [:aws-connect :create-connection] true)
         job-steps (if create-connection ["create-connections"] [])
         dispatch-requests (for [resource resources
                                 :let [agent-id (get agent-assignments (:id resource) "default")
                                       resource-arn (:id resource)
                                       security-group (get security-groups (:id resource) "")
                                       port (get ports (:id resource) "")
                                       connection-prefix (or (get connection-names (:id resource))
                                                             (str (:name resource) "-" (:account-id resource)))]]
                             [:fetch
                              {:method "POST"
                               :uri "/dbroles/jobs"
                               :body {:agent_id agent-id
                                      :aws {:instance_arn resource-arn
                                            :default_security_group (if (empty? security-group)
                                                                      nil
                                                                      {:ingress_cidr security-group
                                                                       :target_port (if (empty? port)
                                                                                      (get default-ports (:engine resource))
                                                                                      (js/parseInt port))})}
                                      :connection_prefix_name (str connection-prefix "-")
                                      :job_steps job-steps}
                               :on-success #(rf/dispatch [:aws-connect/connection-created-success % resource])
                               :on-failure #(rf/dispatch [:aws-connect/connection-created-failure % resource])}])]
     {:db (assoc-in db [:aws-connect :resources :total-to-process] total-resources)
      :dispatch-n dispatch-requests})))

(rf/reg-event-fx
 :aws-connect/connection-created-success
 (fn [{:keys [db]} [_ response resource]]
   (let [resource-id (:id resource)
         connection-name (get-in db [:aws-connect :creation-status :connections resource-id :name])
         updated-db (-> db
                        (assoc-in [:aws-connect :creation-status :connections resource-id :status] "success")
                        (assoc-in [:aws-connect :creation-status :connections resource-id :error] nil))

         all-connections (get-in updated-db [:aws-connect :creation-status :connections])
         all-completed? (every? #(contains? #{"success" "failure"} (:status %))
                                (vals all-connections))]

     {:db (-> updated-db
              (assoc-in [:aws-connect :creation-status :all-completed?] all-completed?))
      :dispatch [:show-snackbar {:level :success
                                 :text (str "Connection " connection-name " created successfully!")}]})))

(rf/reg-event-fx
 :aws-connect/connection-created-failure
 (fn [{:keys [db]} [_ response resource]]
   (let [resource-id (:id resource)
         connection-name (get-in db [:aws-connect :creation-status :connections resource-id :name])
         error-message (or response
                           "Failed to create connection")

         updated-db (-> db
                        (assoc-in [:aws-connect :creation-status :connections resource-id :status] "failure")
                        (assoc-in [:aws-connect :creation-status :connections resource-id :error] error-message))

         all-connections (get-in updated-db [:aws-connect :creation-status :connections])
         all-completed? (every? #(contains? #{"success" "failure"} (:status %))
                                (vals all-connections))]

     {:db (-> updated-db
              (assoc-in [:aws-connect :creation-status :all-completed?] all-completed?))
      :dispatch [:show-snackbar {:level :error
                                 :text (str "Failed to create connection " connection-name)}]})))

;; New events for fetching AWS accounts
(rf/reg-event-fx
 :aws-connect/fetch-accounts
 (fn [{:keys [db]} _]
   {:dispatch [:fetch
               {:method "GET"
                :uri "/integrations/aws/organizations"
                :on-success #(rf/dispatch [:aws-connect/fetch-accounts-success %])
                :on-failure #(rf/dispatch [:aws-connect/fetch-accounts-failure %])}]}))

(rf/reg-event-fx
 :aws-connect/fetch-accounts-success
 (fn [{:keys [db]} [_ response]]
   (let [accounts (get response :items [])]
     {:db (-> db
              (assoc-in [:aws-connect :accounts :data] accounts)
              (assoc-in [:aws-connect :accounts :status] :loaded)
              (assoc-in [:aws-connect :accounts :api-error] nil)
              (assoc-in [:aws-connect :loading :active?] false)
              (assoc-in [:aws-connect :loading :message] nil))
      :dispatch [:aws-connect/set-current-step :accounts]})))

(rf/reg-event-fx
 :aws-connect/fetch-accounts-failure
 (fn [{:keys [db]} [_ response]]
   (let [error-message (or response
                           "Failed to fetch AWS organization accounts")
         api-error {:message error-message
                    :status (get response :status 500)
                    :raw-response (get response :response)}]
     {:db (-> db
              (assoc-in [:aws-connect :accounts :status] :error)
              (assoc-in [:aws-connect :accounts :api-error] api-error)
              (assoc-in [:aws-connect :loading :active?] false)
              (assoc-in [:aws-connect :loading :message] nil))
      :dispatch [:show-snackbar {:level :error
                                 :text "Failed to retrieve AWS accounts. Please check your credentials and try again."}]})))

;; Toggle selection of an account
(rf/reg-event-db
 :aws-connect/toggle-account-selection
 (fn [db [_ account-id selected?]]
   (if selected?
     (update-in db [:aws-connect :accounts :selected] conj account-id)
     (update-in db [:aws-connect :accounts :selected] disj account-id))))

;; Select or deselect all accounts
(rf/reg-event-db
 :aws-connect/select-all-accounts
 (fn [db [_ selected?]]
   (let [accounts (get-in db [:aws-connect :accounts :data] [])
         all-account-ids (map :account_id accounts)]
     (assoc-in db [:aws-connect :accounts :selected]
               (if selected?
                 (set all-account-ids)
                 #{})))))

;; Set the selected accounts
(rf/reg-event-db
 :aws-connect/set-selected-accounts
 (fn [db [_ selected]]
   (assoc-in db [:aws-connect :accounts :selected] selected)))

;; Subscriptions
(rf/reg-sub
 :aws-connect/current-step
 (fn [db _]
   (get-in db [:aws-connect :current-step])))

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

(rf/reg-sub
 :aws-connect/resources-api-error
 (fn [db _]
   (get-in db [:aws-connect :resources :api-error])))

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

(rf/reg-event-db
 :aws-connect/set-security-group
 (fn [db [_ resource-id security-group]]
   (assoc-in db [:aws-connect :resources :security-groups resource-id] security-group)))

(rf/reg-event-db
 :aws-connect/set-port
 (fn [db [_ resource-id port]]
   (assoc-in db [:aws-connect :resources :ports resource-id] port)))

(rf/reg-sub
 :aws-connect/security-groups
 (fn [db _]
   (get-in db [:aws-connect :resources :security-groups] {})))

(rf/reg-sub
 :aws-connect/ports
 (fn [db _]
   (get-in db [:aws-connect :resources :ports] {})))

(rf/reg-sub
 :aws-connect/creation-status
 (fn [db _]
   (get-in db [:aws-connect :creation-status])))

(rf/reg-event-db
 :aws-connect/toggle-create-connection
 (fn [db [_ value]]
   (assoc-in db [:aws-connect :create-connection] value)))

(rf/reg-sub
 :aws-connect/create-connection
 (fn [db _]
   (get-in db [:aws-connect :create-connection] true)))

;; New subscriptions for accounts step
(rf/reg-sub
 :aws-connect/accounts
 (fn [db _]
   (get-in db [:aws-connect :accounts :data])))

(rf/reg-sub
 :aws-connect/selected-accounts
 (fn [db _]
   (get-in db [:aws-connect :accounts :selected])))

(rf/reg-sub
 :aws-connect/accounts-error
 (fn [db _]
   (get-in db [:aws-connect :accounts :api-error :message])))
