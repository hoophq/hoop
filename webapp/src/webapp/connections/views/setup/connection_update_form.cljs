(ns webapp.connections.views.setup.connection-update-form
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Tabs Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.connections.constants :as constants]
   [webapp.connections.views.setup.additional-configuration :as additional-configuration]
   [webapp.connections.views.setup.database :as database]
   [webapp.connections.views.setup.events.process-form :as helpers]
   [webapp.connections.views.setup.network :as network]
   [webapp.connections.views.setup.page-wrapper :as page-wrapper]
   [webapp.connections.views.setup.server :as server]))

(defn update-form-header [{:keys [name type subtype]}]
  [:> Box {:class "pb-[--space-5]"}
   [:> Flex {:justify "between" :align "center"}
    [:> Box {:class "space-y-radix-3"}
     [:> Heading {:size "6" :weight "bold" :class "text-[--gray-12]"} "Configure"]
     [:> Flex {:gap "3" :align "center"}
      [:figure {:class "w-4"}
       [:img {:src (constants/get-connection-icon
                    {:type type
                     :subtype subtype})}]]
      [:> Text {:size "3" :class "text-[--gray-12]"}
       name]]]]])

(defn loading-view []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn main [connection-name]
  (r/with-let
    [active-tab (r/atom "credentials")
     connection (rf/subscribe [:connections->connection-details])
     _ (rf/dispatch [:connections->get-connection-details connection-name])]

    (if (:loading @connection)
      [loading-view]
      (when (:data @connection)
        (let [processed-connection (helpers/process-connection-for-update (:data @connection))]
          (rf/dispatch [:connection-setup/initialize-state processed-connection])

          [page-wrapper/main
           {:children
            [:> Box {:class "min-h-screen py-8 px-6"}
                           ;; Header
             [update-form-header (:data @connection)]

                           ;; Main content
             [:form {:id "update-connection-form"
                     :on-submit (fn [e]
                                  (.preventDefault e)
                                  (rf/dispatch [:connections->update-connection {:name connection-name}]))}
              [:> Tabs.Root {:value @active-tab
                             :on-value-change #(reset! active-tab %)}
               [:> Tabs.List {:mb "7"}
                [:> Tabs.Trigger {:value "credentials"} "Credentials"]
                [:> Tabs.Trigger {:value "configuration"} "Additional Configuration"]]

               [:> Tabs.Content {:value "credentials"}
                (case (:type (:data @connection))
                  "database" [database/credentials-step
                              (:subtype (:data @connection))
                              :update]
                  "custom" [server/credentials-step]
                  "application" [network/credentials-form]
                  nil)]

               [:> Tabs.Content {:value "configuration"}
                [additional-configuration/main
                 {:show-database-schema? (= (:type (:data @connection)) "database")
                  :selected-type (:subtype (:data @connection))
                  :form-type :update}]]]]]

            :footer-props
            {:back-text "Back"
             :next-text "Save"
             :on-back #(js/history.back)
             :on-next (fn []
                        (let [form (.getElementById js/document "update-connection-form")]
                          (when form
                            (.dispatchEvent form (js/Event. "submit" #js{:bubbles true :cancelable true})))))

             :on-delete #(rf/dispatch [:dialog->open
                                       {:title "Delete connection?"
                                        :type :danger
                                        :text-action-button "Confirm and delete"
                                        :action-button? true
                                        :text [:> Box {:class "space-y-radix-4"}
                                               [:> Text {:as "p"}
                                                "This action will instantly remove your access to "
                                                (:name (:data @connection))
                                                " and can not be undone."]
                                               [:> Text {:as "p"}
                                                "Are you sure you want to delete this connection?"]]
                                        :on-success (fn []
                                                      (rf/dispatch [:connections->delete-connection (:name (:data @connection))])
                                                      (rf/dispatch [:modal->close]))}])}}])))

    (finally
      (rf/dispatch [:connection-setup/initialize-state nil]))))
