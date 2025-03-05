(ns webapp.webclient.components.panels.runbooks
  (:require
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.webclient.runbooks.list :as runbooks-list]))

(defn main []
  (let [templates (rf/subscribe [:runbooks-plugin->runbooks])
        filtered-templates (rf/subscribe [:runbooks-plugin->filtered-runbooks])
        search-term (rf/subscribe [:search/term])
        primary-connection (rf/subscribe [:connections/selected])
        selected-connections (rf/subscribe [:connection-selection/selected])
        data-loaded? (r/atom false)]

    (when (and (not @data-loaded?)
               (not= :ready (:status @templates)))
      (reset! data-loaded? true)
      (rf/dispatch [:runbooks-plugin->get-runbooks
                    (map :name (concat
                                (when @primary-connection
                                  [@primary-connection])
                                @selected-connections))]))

    (when (and (not (empty? @search-term))
               (= :ready (:status @templates)))
      (rf/dispatch [:search/filter-runbooks @search-term]))

    {:title "Runbooks"
     :content [runbooks-list/main templates filtered-templates]}))
