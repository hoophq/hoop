(ns webapp.onboarding.mock-data)

(def mock-aws-accounts
  [{:id "012345678901"
    :alias "nobelium-silver-wolf"
    :status "Active"}
   {:id "098765432109"
    :alias "dysprosium-green-cat"
    :status "Active"}
   {:id "086420135791"
    :alias "scandium-black-pelican"
    :status "Inactive"}
   {:id "135791086420"
    :alias "hassium-golden-turtle"
    :status "Active"}])

(def mock-aws-resources
  [{:id "private-app-1a"
    :name "rds-mysql-prod-pri"
    :subnet-cidr "10.0.1.0/24"
    :vpc-id "vpc-0e1b2c3d4e5f67890"
    :status "Active"
    :security-group-enabled? false}
   {:id "private-app-1b"
    :name "rds-mysql-prod-replica"
    :subnet-cidr "10.0.2.0/24"
    :vpc-id "vpc-1a2b3c4d5e6f78901"
    :status "Active"
    :security-group-enabled? true}
   {:id "private-app-1c"
    :name "rds-mysql-staging"
    :subnet-cidr "10.0.3.0/24"
    :vpc-id "vpc-2b3c4d5e6f7890123"
    :status "Active"
    :security-group-enabled? false}
   {:id "private-app-2a"
    :name "rds-mysql-staging-fbsq"
    :subnet-cidr "10.0.4.0/24"
    :vpc-id "vpc-3c4d5e6f78901234"
    :status "Inactive"
    :security-group-enabled? false}])

(def mock-agents
  [{:id "prod-ag-rs19"
    :name "Production Agent RS19"
    :status "Active"}
   {:id "staging-ag-rs20"
    :name "Staging Agent RS20"
    :status "Active"}
   {:id "dev-ag-rs21"
    :name "Development Agent RS21"
    :status "Inactive"}])

(def mock-aws-credentials
  {:type :iam-user
   :iam-role nil
   :iam-user {:access-key-id "AKIAIOSFODNN7EXAMPLE"
              :secret-access-key "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
              :region "us-east-1"
              :session-token nil}})

(def mock-agent-assignments
  {"private-app-1a" "prod-ag-rs19"
   "private-app-1b" "prod-ag-rs19"
   "private-app-1c" "staging-ag-rs20"})
