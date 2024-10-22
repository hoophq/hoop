(ns webapp.plugins.views.plugin-configurations.access-control
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.headings :as h]
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
                               (or (array->select-options
                                    (:config current-connection-config)) [{"value" "admin" "label" "admin"}]))]
    (fn [{:keys [plugin user-groups]}]
      [:section {:class "flex flex-col px-small"}
       [:form
        {:on-submit (fn [e]
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
        [:header
         [:div {:class "font-bold text-xs pb-1"}
          "Access user groups"]
         [:footer {:class "text-xs text-gray-600 pb-4"}
          (str "Selected user groups are able to view and interact with this connection. "
               "Admin is set by default if no user group is selected.")]]
        [multi-select/creatable-select {:options (array->select-options user-groups)
                                        :id "approval-groups-input"
                                        :name "approval-groups-input"
                                        :default-value @approval-groups-value
                                        :on-change #(reset! approval-groups-value (js->clj %))}]
        [:footer
         {:class "flex justify-end"}
         [:div {:class "flex-shrink"}
          [button/primary {:text "Save"
                           :variant :small
                           :type "submit"}]]]]])))

(defn plugin-configuration-container-not-installed []
  [:section
   [:div {:class "grid grid-cols-2 items-center gap-large"}
    [:div
     [h/h3 "Enable Access Control" {:class "text-gray-800"}]
     [:span {:class "block text-sm mb-regular text-gray-600"}
      (str "Activate to enable an additional security layer."
           " When activated users are not allowed to access connections"
           " by default unless permission is given for each one.")]]
    [button/primary
     {:text "Activate"
      :variant :small
      :on-click #(rf/dispatch
                  [:dialog->open
                   {:title "Activate Access Control"
                    :text (str "By activating this feature users will have"
                               " their accesses blocked until a connection permission is set.")
                    :text-action-button "Confirm"
                    :type :info
                    :on-success (fn []
                                  (rf/dispatch [:plugins->create-plugin {:name "access_control"
                                                                         :connections []}])
                                  (js/setTimeout
                                   (fn [] (rf/dispatch [:plugins->get-plugin-by-name "access_control"]))
                                   1000))}])}]]])

(defn main [installed?]
  (if (not installed?)
    [plugin-configuration-container-not-installed]
    [plugin-configuration-container/main configuration-modal]))

