(ns webapp.onboarding.setup-resource
  (:require
   [webapp.connections.views.setup.main :as setup]))

(defn main []
  [setup/main :onboarding])
