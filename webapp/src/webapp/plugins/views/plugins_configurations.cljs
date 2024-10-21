(ns webapp.plugins.views.plugins-configurations
  (:require
   [webapp.plugins.views.plugin-configurations.container :as plugin-configuration-container]
   [webapp.plugins.views.plugin-configurations.access-control :as access-control-config]
   [webapp.plugins.views.plugin-configurations.audit :as audit-config]
   [webapp.plugins.views.plugin-configurations.runbooks :as runbooks-config]
   [webapp.plugins.views.plugin-configurations.editor :as editor-config]
   [webapp.plugins.views.plugin-configurations.indexer :as indexer-config]
   [webapp.plugins.views.plugin-configurations.slack :as slack-config]
   [webapp.plugins.views.plugin-configurations.jira :as jira-config]
   [webapp.plugins.views.plugin-configurations.ask-ai :as ask-ai-config]))

(defmulti config identity)
(defmethod config "audit" []
  [audit-config/main])
(defmethod config "access_control" []
  [access-control-config/main true])
(defmethod config "access_control-not-installed" []
  [access-control-config/main false])
(defmethod config "runbooks" []
  [runbooks-config/main])
(defmethod config "editor" []
  [editor-config/main])
(defmethod config "indexer" []
  [indexer-config/main])
(defmethod config "slack" []
  [slack-config/main])
(defmethod config "jira" []
  [jira-config/main])
(defmethod config "ask_ai" []
  [ask-ai-config/main])
(defmethod config :default []
  [plugin-configuration-container/main])

