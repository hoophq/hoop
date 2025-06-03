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
     credentials-valid? (r/atom false)
     connection (rf/subscribe [:connections->connection-details])
     guardrails-list (rf/subscribe [:guardrails->list])
     jira-templates-list (rf/subscribe [:jira-templates->list])
     initialized? (r/atom false)
     check-form-validity! (fn []
                            (when-let [form (.getElementById js/document "credentials-form")]
                              (.reportValidity form)
                              (reset! credentials-valid? (.checkValidity form))))
     _ (rf/dispatch [:connections->get-connection-details connection-name])
     _ (rf/dispatch [:guardrails->get-all])
     _ (rf/dispatch [:jira-templates->get-all])]

    (when-let [form (.getElementById js/document "credentials-form")]
      (reset! credentials-valid? (.checkValidity form)))

    (let [handle-submit (fn [e]
                          (.preventDefault e)
                          (let [form (.getElementById js/document "credentials-form")
                                current-env-key @(rf/subscribe [:connection-setup/env-current-key])
                                current-env-value @(rf/subscribe [:connection-setup/env-current-value])
                                current-file-name @(rf/subscribe [:connection-setup/config-current-name])
                                current-file-content @(rf/subscribe [:connection-setup/config-current-content])
                                current-tag-key @(rf/subscribe [:connection-setup/current-key])
                                current-tag-value @(rf/subscribe [:connection-setup/current-value])]

                            (when form
                              (.reportValidity form)
                              (reset! credentials-valid? (.checkValidity form)))

                            (when @credentials-valid?
                              ;; Process current input values before submitting
                              (when (and (not (empty? current-env-key))
                                         (not (empty? current-env-value)))
                                (doall
                                 (rf/dispatch [:connection-setup/update-env-var
                                               (count @(rf/subscribe [:connection-setup/environment-variables]))
                                               :key
                                               current-env-key])
                                 (rf/dispatch [:connection-setup/update-env-var
                                               (count @(rf/subscribe [:connection-setup/environment-variables]))
                                               :value
                                               current-env-value])))

                              (when (and (not (empty? current-file-name))
                                         (not (empty? current-file-content)))
                                (doall
                                 (rf/dispatch [:connection-setup/update-config-file
                                               (count @(rf/subscribe [:connection-setup/configuration-files]))
                                               :key
                                               current-file-name])
                                 (rf/dispatch [:connection-setup/update-config-file
                                               (count @(rf/subscribe [:connection-setup/configuration-files]))
                                               :value
                                               current-file-content])))

                              ;; Process current tag values before submitting
                              (when (and current-tag-key (.-value current-tag-key))
                                (rf/dispatch [:connection-setup/add-tag
                                              (.-value current-tag-key)
                                              (if current-tag-value
                                                (.-value current-tag-value)
                                                "")]))

                              ;; Submit the form
                              (rf/dispatch [:connections->update-connection {:name connection-name}]))

                            (when (not @credentials-valid?)
                              (reset! active-tab "credentials"))))]

      (r/create-class
       {:component-did-mount check-form-validity!

        :component-did-update
        (fn [this old-argv]
          (let [[_ prev-connection-name] old-argv
                [_ curr-connection-name] (r/argv this)]
            (when (not= prev-connection-name curr-connection-name)
              (check-form-validity!))))

        :reagent-render
        (fn []
          (if (:loading @connection)
            [loading-view]
            (when (:data @connection)
              (when (and (not @initialized?)
                         (:data @connection))
                (let [processed-connection (helpers/process-connection-for-update
                                            (:data @connection)
                                            (:data @guardrails-list)
                                            (:data @jira-templates-list))]
                  (rf/dispatch [:connection-setup/initialize-state processed-connection])
                  (reset! initialized? true)
                  (js/setTimeout check-form-validity! 100)))

              [page-wrapper/main
               {:children
                [:> Box {:class "min-h-screen py-8 px-6"}
                 [update-form-header (:data @connection)]

                 [:form {:id "update-connection-form"
                         :on-submit handle-submit}
                  [:> Tabs.Root {:value @active-tab
                                 :on-value-change (fn [new-tab]
                                                    (when (and (= @active-tab "credentials")
                                                               (not= new-tab "credentials"))
                                                      (check-form-validity!))
                                                    (reset! active-tab new-tab))}
                   [:> Tabs.List {:mb "7"}
                    [:> Tabs.Trigger {:value "credentials"} "Credentials"]
                    [:> Tabs.Trigger {:value "configuration"} "Additional Configuration"]]

                   [:> Tabs.Content {:value "credentials"}
                    (case (:type (:data @connection))
                      "database" [database/credentials-step
                                  (:subtype (:data @connection))
                                  :update]
                      "custom" [server/credentials-step]
                      "application" (if (= (:subtype (:data @connection)) "ssh")
                                      [server/ssh-credentials]
                                      [network/credentials-form
                                       {:connection-type (:subtype (:data @connection))}])
                      nil)]

                   [:> Tabs.Content {:value "configuration"}
                    [additional-configuration/main
                     {:show-database-schema? (or (= (:type (:data @connection)) "database")
                                                 (= (:subtype (:data @connection)) "dynamodb"))
                      :selected-type (:subtype (:data @connection))
                      :form-type :update}]]]]]

                :footer-props
                {:form-type :update
                 :back-text "Back"
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
                                                          (rf/dispatch [:modal->close]))}])}}])))}))
    (finally
      (rf/dispatch [:connection-setup/initialize-state nil])
      (rf/dispatch [:connections->clear-connection-details]))))
