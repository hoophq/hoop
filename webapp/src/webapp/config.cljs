(ns webapp.config
  (:require ["./version.js" :as version]
            [webapp.env :as env]))

(def debug?
  ^boolean goog.DEBUG)

(def app-version version)
(println :release env/release-type app-version)

(def release-type env/release-type)
(def api env/api-url)

(def webapp-url env/webapp-url)
(def hoop-app-url env/hoop-app-url)

; TODO remove all these values from and use dotenv (or smt similar)
(def launch-darkly-client-id "614b7c6f3576ea0d34af1b0c")

(def segment-write-key env/segment-write-key)
(def canny-id env/canny-id)

(def sentry-sample-rate env/sentry-sample-rate)
(def sentry-dsn env/sentry-dsn)

(def docs-url
  {:concepts {:agents "https://hoop.dev/docs/concepts/agents"
              :connections "https://hoop.dev/docs/concepts/connections"}
   :features {:runbooks "https://hoop.dev/docs/learn/features/runbooks"
              :ai-datamasking "https://hoop.dev/docs/learn/features/ai-data-masking"
              :access-control "https://hoop.dev/docs/learn/features/access-control"
              :jit-reviews "https://hoop.dev/docs/learn/features/reviews/jit-reviews"
              :command-reviews "https://hoop.dev/docs/learn/features/reviews/command-reviews"
              :guardrails "https://hoop.dev/docs/learn/features/guardrails"}
   :introduction {:getting-started "https://hoop.dev/docs/introduction/getting-started"}
   :quickstart {:databases "https://hoop.dev/docs/quickstart/databases"
                :cloud-services "https://hoop.dev/docs/quickstart/cloud-services"
                :web-applications "https://hoop.dev/docs/quickstart/web-applications"
                :development-environments "https://hoop.dev/docs/quickstart/development-environments"
                :ssh "https://hoop.dev/docs/quickstart/ssh"}
   :setup {:architecture "https://hoop.dev/docs/setup/architecture"
           :deployment {:overview "https://hoop.dev/docs/setup/deployment"
                        :kubernetes "https://hoop.dev/docs/setup/deployment/kubernetes"
                        :docker "https://hoop.dev/docs/setup/deployment/docker-compose"
                        :aws "https://hoop.dev/docs/setup/deployment/AWS"
                        :on-premises "https://hoop.dev/docs/setup/deployment/on-premises"}
           :configuration {:overview "https://hoop.dev/docs/setup/configuration"
                           :environment-variables "https://hoop.dev/docs/setup/configuration/environment-variables"
                           :reverse-proxy "https://hoop.dev/docs/setup/configuration/reverse-proxy"
                           :identity-providers "https://hoop.dev/docs/setup/configuration/idp/get-started"
                           :secrets-manager "https://hoop.dev/docs/setup/configuration/secrets-manager-configuration"
                           :ai-data-masking "https://hoop.dev/docs/setup/configuration/ai-data-masking"}
           :apis {:api-keys "https://hoop.dev/docs/setup/apis/api-key#api-key"
                  :overview "https://hoop.dev/docs/setup/apis"}
           :license-management "https://hoop.dev/docs/setup/license-management"}
   :clients {:web-app {:overview "https://hoop.dev/docs/clients/webapp/overview"
                       :creating-connection "https://hoop.dev/docs/clients/webapp/creating-connection"
                       :managing-accesss "https://hoop.dev/docs/clients/webapp/managing-accesss"
                       :monitoring-sessions "https://hoop.dev/docs/clients/webapp/monitoring-sessions"}
             :desktop-app "https://hoop.dev/docs/clients/desktop-app"
             :command-line {:overview "https://hoop.dev/docs/clients/cli"
                            :windows "https://hoop.dev/docs/clients/cli#windows"
                            :macos "https://hoop.dev/docs/clients/cli#mac-os"
                            :linux "https://hoop.dev/docs/clients/cli#linux"
                            :managing-configuration "https://hoop.dev/docs/clients/cli#managing-configuration"}}
   :integrations {:slack "https://hoop.dev/docs/integrations/slack"
                  :teams "https://hoop.dev/docs/integrations/teams"
                  :jira "https://hoop.dev/docs/integrations/jira"
                  :svix "https://hoop.dev/docs/integrations/svix"
                  :aws-connect "https://hoop.dev/docs/integrations/aws"}})
