(ns webapp.webclient.components.panels.runbooks
  (:require
   [re-frame.core :as rf]
   [webapp.webclient.runbooks.list :as runbooks-list]))

(defn main []
  (let [templates (rf/subscribe [:runbooks-plugin->runbooks])
        filtered-templates (rf/subscribe [:runbooks-plugin->filtered-runbooks])]

    (rf/dispatch [:runbooks-plugin->get-runbooks []])

    {:title "Runbooks"
     :content [runbooks-list/main templates filtered-templates]}))
