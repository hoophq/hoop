(ns webapp.connections.views.setup.connection-update-form
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Tabs Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.connections.constants :as constants]
   [webapp.connections.views.setup.database :as database]
   [webapp.connections.views.setup.additional-configuration :as additional-configuration]
   [webapp.connections.views.setup.events.process-form :as helpers]))

(defn update-form-header [{:keys [name type subtype]}]
  [:> Box {:class "border-b border-[--gray-a6] pb-[--space-5] top-0 z-50 bg-white p-10"}
   [:> Flex {:justify "between" :align "center"}
    [:> Flex {:gap "3" :align "center"}
     [:figure {:class "w-6"}
      [:img {:src (constants/get-connection-icon
                   {:type type
                    :subtype subtype})}]]
     [:> Box
      [:> Heading {:size "8" :class "text-gray-12"} "Configure"]
      [:> Text {:size "5" :class "text-gray-11"} name]]]

    [:> Flex {:gap "5" :align "center"}
     [:> Button {:variant "ghost"
                 :color "red"
                 :type "button"
                 :on-click #(rf/dispatch [:dialog->open
                                          {:title "Delete connection?"
                                           :type :danger
                                           :text-action-button "Confirm and delete"
                                           :action-button? true
                                           :text [:> Box {:class "space-y-radix-4"}
                                                  [:> Text {:as "p"}
                                                   "This action will instantly remove your access to "
                                                   name
                                                   " and can not be undone."]
                                                  [:> Text {:as "p"}
                                                   "Are you sure you want to delete this connection?"]]
                                           :on-success (fn []
                                                         (rf/dispatch [:connections->delete-connection name])
                                                         (rf/dispatch [:modal->close]))}])}
      "Delete"]
     [:> Button {:type "submit"
                 :form "update-connection-form"}
      "Save"]]]])

(defn loading-view []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn main [connection-name]
  (let [active-tab (r/atom "credentials")
        connection (rf/subscribe [:connections->connection-details])]

    (rf/dispatch [:connections->get-connection-details connection-name])

    (fn []
      (if (:loading @connection)
        [loading-view]
        (when (:data @connection)
          (let [processed-connection (helpers/process-connection-for-update (:data @connection))]
            ;; Só inicializa o estado quando tiver a conexão carregada
            (rf/dispatch [:connection-setup/initialize-state processed-connection])

            [:> Box {:class "min-h-screen bg-gray-1"}
             ;; Header
             [update-form-header (:data @connection)]

             ;; Main content
             [:> Box {:class "p-8"}
              [:> Tabs.Root {:value @active-tab
                             :on-value-change #(reset! active-tab %)}
               [:> Tabs.List
                [:> Tabs.Trigger {:value "credentials"} "Credentials"]
                [:> Tabs.Trigger {:value "configuration"} "Additional Configuration"]]

               [:> Tabs.Content {:value "credentials"}
                (case (:type (:data @connection))
                  "database" [database/credentials-step
                              (:subtype (:data @connection))
                              @(rf/subscribe [:connection-setup/database-credentials])]
                  nil)]

               [:> Tabs.Content {:value "configuration"}
                [:form {:id "update-connection-form"
                        :on-submit (fn [e]
                                     (.preventDefault e)
                                     (let [form-data @(rf/subscribe [:connection-setup/form-data])
                                           updated-connection (helpers/process-connection-for-save
                                                               form-data
                                                               (:data @connection))]
                                       (rf/dispatch [:connections->update-connection updated-connection])))}
                 [additional-configuration/main
                  {:show-database-schema? (= (:type (:data @connection)) "database")
                   :selected-type (:subtype (:data @connection))}]]]]]]))))))
