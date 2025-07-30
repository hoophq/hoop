(ns webapp.webclient.components.panels.runbooks
  (:require
   [re-frame.core :as rf]
   [webapp.webclient.runbooks.list :as runbooks-list]))

(rf/reg-event-db
 :runbooks/set-data-loaded
 (fn [db [_ value]]
   (assoc-in db [:runbooks :data-loaded] value)))

(rf/reg-sub
 :runbooks/data-loaded
 (fn [db]
   (get-in db [:runbooks :data-loaded] false)))

(defn main []
  (let [templates (rf/subscribe [:runbooks-plugin->runbooks])
        filtered-templates (rf/subscribe [:runbooks-plugin->filtered-runbooks])
        search-term (rf/subscribe [:search/term])
        primary-connection (rf/subscribe [:primary-connection/selected])
        selected-connections (rf/subscribe [:multiple-connections/selected])
        data-loaded? (rf/subscribe [:runbooks/data-loaded])]

    (when (and (not @data-loaded?)
               (not= :ready (:status @templates)))
      (rf/dispatch [:runbooks/set-data-loaded true])
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
