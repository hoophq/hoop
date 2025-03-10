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
  [{:id "rds-mysql-prod-pri"
    :name "rds-mysql-prod-pri"
    :subnet-cidr "10.0.1.0/24"
    :vpc-id "vpc-0e1b2c3d4e5f67890"
    :status "Active"}
   {:id "rds-mysql-prod-replica"
    :name "rds-mysql-prod-replica"
    :subnet-cidr "10.0.1.0/24"
    :vpc-id "vpc-0e1b2c3d4e5f67890"
    :status "Active"}
   {:id "rds-mysql-staging"
    :name "rds-mysql-staging"
    :subnet-cidr "10.0.1.0/24"
    :vpc-id "vpc-0e1b2c3d4e5f67890"
    :status "Active"
    :error {:message "User: arn:aws:iam::1234567890123:user/TestUser is not authorized to perform: rds:DescribeDBInstances on resource: arn:aws:rds:us-east-1:1234567890123:db:rds-mysql-staging with an explicit deny",
            :code "AccessDenied",
            :type "Sender"}}
   {:id "rds-mysql-staging-2d-sq"
    :name "rds-mysql-staging-2d-sq"
    :subnet-cidr "10.0.2.0/24"
    :vpc-id "vpc-1a2b3c4d5e6f78901"
    :status "Inactive"}
   {:id "rds-mysql-staging-2d-rw"
    :name "rds-mysql-staging-2d-rw"
    :subnet-cidr "10.0.2.0/24"
    :vpc-id "vpc-1a2b3c4d5e6f78901"
    :status "Active"}])

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
  {"rds-mysql-prod-pri" "prod-ag-rs19"
   "rds-mysql-prod-replica" "prod-ag-rs19"
   "rds-mysql-staging" "staging-ag-rs20"})
