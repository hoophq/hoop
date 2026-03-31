(ns webapp.features.machine-identities.events
  (:require
   [re-frame.core :as rf]))

(def mock-identities
  [{:id "id-1"
    :name "backend-prod"
    :description "Python main service"
    :type "aws-iam"
    :connection-names ["aws-prod-postgres"]
    :attributes ["production" "api" "backend"]
    :created-at "2026-03-15T10:00:00Z"
    :status "active"
    :roles
    [{:id "id-1-r1"
      :name "postgres-logistica-stg-postgresontimesqmrpprdbrs909-ontimesqmrpprd-rw"
      :resource-role "aws-prod-postgres"
      :attributes ["production" "readonly"]
      :status :online
      :connection-stub {:type "database" :subtype "postgres"}
      :credentials {:database-name "dellstore"
                    :host "127.0.0.1"
                    :username "pgaccess-8b04la_FUAS-AS91HAS9hsA0sSnasaAlyZ"
                    :password "noop"
                    :port "5432"}
      :connection-uri "postgresql://pgaccess-8b04la_FUAS-AS91HAS9hsA0sSnasaAlyZ@127.0.0.1:5432/dellstore"}
     {:id "id-1-r2"
      :name "service-connection-logistics-stage-oracle-analytics-01"
      :resource-role "aws-prod-postgres"
      :attributes ["analytics"]
      :status :online
      :connection-stub {:type "database" :subtype "oracledb"}
      :credentials {:database-name "ORCL"
                    :host "oracle.internal.corp"
                    :username "svc_analytics"
                    :password "••••••••"
                    :port "1521"}
      :connection-uri "oracle://svc_analytics@oracle.internal.corp:1521/ORCL"}
     {:id "id-1-r3"
      :name "mssql-logistica-stg-sqlserverontimesqmrpprdbrs"
      :resource-role "aws-prod-postgres"
      :attributes ["production"]
      :status :online
      :connection-stub {:type "database" :subtype "mssql"}
      :credentials {:database-name "LogisticsDW"
                    :host "mssql.internal.corp"
                    :username "svc_mssql_rw"
                    :password "••••••••"
                    :port "1433"}
      :connection-uri "sqlserver://svc_mssql_rw@mssql.internal.corp:1433/LogisticsDW"}]}
   {:id "id-2"
    :name "payments-prod"
    :description "Payment processing workers"
    :type "gcp-service-account"
    :connection-names ["gcp-bigquery"]
    :attributes ["analytics" "readonly"]
    :created-at "2026-03-20T14:30:00Z"
    :status "active"
    :roles
    [{:id "id-2-r1"
      :name "bq-payments-curated-dataset-rw"
      :resource-role "gcp-bigquery"
      :attributes ["analytics" "readonly"]
      :status :online
      :connection-stub {:type "database" :subtype "postgres"}
      :credentials {:database-name "payments_curated"
                    :host "10.0.2.12"
                    :username "bq_svc_payments"
                    :password "••••"
                    :port "5432"}
      :connection-uri "postgresql://bq_svc_payments@10.0.2.12:5432/payments_curated"}]}
   {:id "id-3"
    :name "azure-storage-identity"
    :description "Managed identity for Azure Blob Storage access"
    :type "azure-managed-identity"
    :connection-names ["azure-storage"]
    :attributes ["storage" "production"]
    :created-at "2026-03-25T09:15:00Z"
    :status "active"
    :roles
    [{:id "id-3-r1"
      :name "blob-archive-west-readwrite"
      :resource-role "azure-storage"
      :attributes ["storage" "production"]
      :status :online
      :connection-stub {:type "database" :subtype "mongodb"}
      :credentials {:database-name "archive"
                    :host "blob.core.windows.net"
                    :username "mi-storage-reader"
                    :password "••••"
                    :port "443"}
      :connection-uri "https://archive.blob.core.windows.net"}]}
   {:id "id-4"
    :name "github-actions-token"
    :description "Token for CI/CD pipeline automation"
    :type "generic"
    :connection-names ["github-api"]
    :attributes ["ci-cd" "automation"]
    :created-at "2026-03-28T16:45:00Z"
    :status "active"
    :roles []}
   {:id "id-5"
    :name "k8s-service-account"
    :description "Kubernetes service account for microservices"
    :type "kubernetes-sa"
    :connection-names ["k8s-prod-cluster"]
    :attributes ["kubernetes" "production" "microservices"]
    :created-at "2026-03-29T11:20:00Z"
    :status "active"
    :roles
    [{:id "id-5-r1"
      :name "mysql-orders-prod-rw"
      :resource-role "k8s-prod-cluster"
      :attributes ["kubernetes" "production"]
      :status :online
      :connection-stub {:type "database" :subtype "mysql"}
      :credentials {:database-name "orders"
                    :host "mysql.prod.svc.cluster.local"
                    :username "k8s_orders"
                    :password "••••"
                    :port "3306"}
      :connection-uri "mysql://k8s_orders@mysql.prod.svc.cluster.local:3306/orders"}]}
   {:id "id-6"
    :name "datadog-monitoring"
    :description "Monitoring service credentials"
    :type "generic"
    :connection-names ["datadog-api"]
    :attributes ["monitoring" "observability"]
    :created-at "2026-03-30T08:30:00Z"
    :status "active"
    :roles []}
   {:id "id-7"
    :name "expired-dev-token"
    :description "Development environment token (expired)"
    :type "generic"
    :connection-names ["dev-api"]
    :attributes ["development" "deprecated"]
    :created-at "2026-02-01T10:00:00Z"
    :status "expired"
    :roles
    [{:id "id-7-r1"
      :name "dev-api-legacy-readonly"
      :resource-role "dev-api"
      :attributes ["development"]
      :status :offline
      :connection-stub {:type "database" :subtype "postgres"}
      :credentials {:database-name "dev"
                    :host "127.0.0.1"
                    :username "dev_reader"
                    :password "expired"
                    :port "5432"}
      :connection-uri "postgresql://dev_reader@127.0.0.1:5432/dev"}]}])

(rf/reg-event-fx
 :machine-identities/list
 (fn [{:keys [db]} [_]]
   {:db (assoc-in db [:machine-identities :status] :loading)
    :fx [[:dispatch-later {:ms 500
                           :dispatch [:machine-identities/set-identities mock-identities]}]]}))

(rf/reg-event-db
 :machine-identities/set-identities
 (fn [db [_ identities]]
   (-> db
       (assoc-in [:machine-identities :data] identities)
       (assoc-in [:machine-identities :status] :success))))

(rf/reg-event-fx
 :machine-identities/get-identity
 (fn [{:keys [db]} [_ identity-id]]
   (let [identity (first (filter #(= (:id %) identity-id)
                                 (get-in db [:machine-identities :data])))]
     {:db (assoc-in db [:machine-identities :current-identity] identity)})))

(rf/reg-event-fx
 :machine-identities/create
 (fn [{:keys [db]} [_ identity-data]]
   (let [new-identity (assoc identity-data
                             :id (str "id-" (random-uuid))
                             :created-at (.toISOString (js/Date.))
                             :status "active"
                             :roles [])
         current-identities (get-in db [:machine-identities :data])]
     {:db (assoc-in db [:machine-identities :data] (conj current-identities new-identity))
      :fx [[:dispatch [:show-snackbar {:level :success
                                       :text "Machine identity created successfully"}]]
           [:dispatch [:navigate :machine-identities]]]})))

(rf/reg-event-fx
 :machine-identities/update
 (fn [{:keys [db]} [_ identity-id identity-data]]
   (let [current-identities (get-in db [:machine-identities :data])
         updated-identities (mapv #(if (= (:id %) identity-id)
                                     (merge % identity-data)
                                     %)
                                  current-identities)]
     {:db (assoc-in db [:machine-identities :data] updated-identities)
      :fx [[:dispatch [:show-snackbar {:level :success
                                       :text "Machine identity updated successfully"}]]
           [:dispatch [:navigate :machine-identities]]]})))

(rf/reg-event-fx
 :machine-identities/delete
 (fn [{:keys [db]} [_ identity-id]]
   (let [current-identities (get-in db [:machine-identities :data])
         filtered-identities (filterv #(not= (:id %) identity-id) current-identities)]
     {:db (assoc-in db [:machine-identities :data] filtered-identities)
      :fx [[:dispatch [:show-snackbar {:level :success
                                       :text "Machine identity deleted successfully"}]]]})))

(rf/reg-event-db
 :machine-identities/clear-current-identity
 (fn [db [_]]
   (assoc-in db [:machine-identities :current-identity] nil)))

(rf/reg-event-db
 :machine-identities/set-status
 (fn [db [_ status]]
   (assoc-in db [:machine-identities :status] status)))
