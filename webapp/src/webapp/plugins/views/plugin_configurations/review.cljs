(ns webapp.plugins.views.plugin-configurations.review
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.multiselect :as multi-select]
            [webapp.plugins.views.plugin-configurations.container :as plugin-configuration-container]))

(defn array->select-options [array]
  (mapv #(into {} {"value" % "label" %}) array))

(defn js-select-options->list [options]
  (mapv #(get % "value") options))

(defn configuration-modal [{:keys [connection plugin]}]
  (let [current-connection-config (or (first (filter #(= (:id connection)
                                                         (:id %))
                                                     (:connections plugin)))
                                      {:id (:id connection)})
        approval-groups-value (r/atom
                               (or (array->select-options (:config current-connection-config))
                                   [{"value" "admin" "label" "admin"}]))]
    (fn [{:keys [plugin user-groups]}]
      [:form {:class "flex flex-col px-small pt-regular h-full justify-between"
              :on-submit (fn [e]
                           (.preventDefault e)
                           (let [connection (merge current-connection-config
                                                   {:config (js-select-options->list
                                                             @approval-groups-value)})
                                 dissoced-connections (filter #(not= (:id %)
                                                                     (:id connection))
                                                              (:connections plugin))
                                 new-plugin-data (assoc plugin :connections (conj
                                                                             dissoced-connections
                                                                             connection))]
                             (rf/dispatch [:plugins->update-plugin new-plugin-data])))}
       [:div {:class "flex flex-col"}
        [:label {:class "font-bold text-xs"}
         "Approval groups"]
        [:span {:class "text-xs text-gray-600 pb-1"}
         (str "Groups that will be asked to review anything on this connection")]
        [multi-select/main {:options (array->select-options user-groups)
                            :id "approval-groups-input"
                            :name "approval-groups-input"
                            :default-value @approval-groups-value
                            :on-change #(reset! approval-groups-value (js->clj %))}]]
       [:div {:class "self-end"}
        [button/primary {:text "Save"
                         :variant :small
                         :type "submit"}]]])))

(defn main []
  [plugin-configuration-container/main configuration-modal])
