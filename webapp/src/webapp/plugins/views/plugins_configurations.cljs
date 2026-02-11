(ns webapp.plugins.views.plugins-configurations
  (:require
   [webapp.plugins.views.plugin-configurations.container :as plugin-configuration-container]
   [webapp.plugins.views.plugin-configurations.slack :as slack-config]))

(defmulti config identity)
(defmethod config "slack" []
  [slack-config/main])
(defmethod config :default []
  [plugin-configuration-container/main])

