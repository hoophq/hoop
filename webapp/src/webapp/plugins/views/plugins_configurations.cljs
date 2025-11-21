(ns webapp.plugins.views.plugins-configurations
  (:require
   [webapp.plugins.views.plugin-configurations.container :as plugin-configuration-container]
   [webapp.plugins.views.plugin-configurations.access-control :as access-control-config]
   [webapp.plugins.views.plugin-configurations.slack :as slack-config]
   [webapp.plugins.views.plugin-configurations.jira :as jira-config]
   [webapp.plugins.views.plugin-configurations.ask-ai :as ask-ai-config]))

(defmulti config identity)
(defmethod config "access_control" []
  [access-control-config/main true])
(defmethod config "access_control-not-installed" []
  [access-control-config/main false])
(defmethod config "slack" []
  [slack-config/main])
(defmethod config "jira" []
  [jira-config/main])
(defmethod config "ask_ai" []
  [ask-ai-config/main])
(defmethod config :default []
  [plugin-configuration-container/main])

