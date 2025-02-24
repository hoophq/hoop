(ns webapp.webclient.components.panels.runbooks
  (:require
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.webclient.runbooks.list :as runbooks-list]))

(defn main []
  (let [templates (rf/subscribe [:runbooks-plugin->runbooks])
        filtered-templates (rf/subscribe [:runbooks-plugin->filtered-runbooks])
        search-term (rf/subscribe [:search/term])
        data-loaded? (r/atom false)] ;; Adicionar flag para controlar carregamento

    (when (and (not @data-loaded?)
               (not= :ready (:status @templates)))
      (reset! data-loaded? true)
      (rf/dispatch [:runbooks-plugin->get-runbooks []]))

        ;; Aplicar filtro quando houver um termo de busca e os templates estiverem carregados
    (when (and (not (empty? @search-term))
               (= :ready (:status @templates)))
      (rf/dispatch [:search/filter-runbooks @search-term]))

    {:title "Runbooks"
     :content [runbooks-list/main templates filtered-templates]}))
