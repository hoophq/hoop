(ns webapp.resources.views.configure-role.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Tabs Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.connections.constants :as constants]
   [webapp.connections.helpers :refer [can-test-connection? is-connection-testing?]]
   [webapp.connections.views.setup.events.process-form :as helpers]
   [webapp.connections.views.setup.page-wrapper :as page-wrapper]
   [webapp.connections.views.test-connection-modal :as test-connection-modal]
   [webapp.resources.views.configure-role.details-tab :as details-tab]
   [webapp.resources.views.configure-role.credentials-tab :as credentials-tab]
   [webapp.resources.views.configure-role.terminal-access-tab :as terminal-access-tab]
   [webapp.resources.views.configure-role.native-access-tab :as native-access-tab]))

(defn header [{:keys [name type subtype]} test-connection-state]
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
       name]]]
    (when (can-test-connection? {:type type :subtype subtype})
      [:> Button {:variant "soft"
                  :color "gray"
                  :on-click #(rf/dispatch [:connections->test-connection name])
                  :disabled (is-connection-testing? test-connection-state name)}
       "Test Connection"])]])

(defn loading-view []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn main [connection-name]
  (r/with-let
    [active-tab (r/atom "details")
     credentials-valid? (r/atom false)
     connection (rf/subscribe [:connections->connection-details])
     guardrails-list (rf/subscribe [:guardrails->list])
     jira-templates-list (rf/subscribe [:jira-templates->list])
     test-connection-state (rf/subscribe [:connections->test-connection])
     initialized? (r/atom false)
     check-form-validity! (fn []
                            (when-let [connection-data (:data @connection)]
                              (let [connection-type (:type connection-data)
                                    connection-subtype (:subtype connection-data)
                                    form-id (cond
                                              (and (= connection-type "application")
                                                   (= connection-subtype "ssh")) "ssh-credentials-form"
                                              :else "credentials-form")
                                    form (.getElementById js/document form-id)]
                                (when form
                                  (.reportValidity form)
                                  (reset! credentials-valid? (.checkValidity form))))))
     _ (rf/dispatch-sync [:connections->get-connection-details connection-name])
     _ (rf/dispatch [:guardrails->get-all])
     _ (rf/dispatch [:jira-templates->get-all])]

    (let [handle-submit (fn [e]
                          (.preventDefault e)
                          (let [connection-type (:type (:data @connection))
                                connection-subtype (:subtype (:data @connection))
                                form-id (cond
                                          (and (= connection-type "application")
                                               (= connection-subtype "ssh")) "ssh-credentials-form"
                                          :else "credentials-form")
                                form (.getElementById js/document form-id)
                                current-tag-key @(rf/subscribe [:connection-setup/current-key])
                                current-tag-value @(rf/subscribe [:connection-setup/current-value])]

                            (when form
                              (.reportValidity form)
                              (reset! credentials-valid? (.checkValidity form)))

                            (when @credentials-valid?
                              ;; Para conexões SSH, limpar campos não utilizados baseado no método de autenticação
                              (when (and (= connection-type "application")
                                         (= connection-subtype "ssh"))
                                (let [auth-method @(rf/subscribe [:connection-setup/ssh-auth-method])]
                                  (case auth-method
                                    "password"
                                    (rf/dispatch [:connection-setup/update-ssh-credentials
                                                  "authorized_server_keys" ""])
                                    "key"
                                    (rf/dispatch [:connection-setup/update-ssh-credentials
                                                  "pass" ""])
                                    nil)))

                              ;; Process current tag values before submitting
                              (when (and current-tag-key (.-value current-tag-key))
                                (rf/dispatch [:connection-setup/add-tag
                                              (.-value current-tag-key)
                                              (if current-tag-value
                                                (.-value current-tag-value)
                                                "")]))

                              ;; Submit the form - use custom event to redirect back to resource
                              (rf/dispatch [:resources->update-role-connection {:name connection-name}]))

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
          (if (or (:loading @connection)
                  (= (:status @guardrails-list) :loading)
                  (= (:status @jira-templates-list) :loading))
            [loading-view]
            (when (:data @connection)

              (when (not @initialized?)
                (let [processed-connection (helpers/process-connection-for-update
                                            (:data @connection)
                                            (:data @guardrails-list)
                                            (:data @jira-templates-list))]
                  (rf/dispatch [:connection-setup/initialize-state processed-connection])
                  (reset! initialized? true)
                  (js/setTimeout check-form-validity! 100)))

              [:div
               [page-wrapper/main
                {:children
                 [:> Box {:class "bg-gray-1 min-h-screen px-4 py-10 sm:px-6 lg:px-20 lg:pt-6 lg:pb-10"}
                  [header (:data @connection) @test-connection-state]

                  [:form {:id "update-connection-form"
                          :on-submit handle-submit}
                   [:> Tabs.Root {:value @active-tab
                                  :on-value-change (fn [new-tab]
                                                     (when (and (= @active-tab "credentials")
                                                                (not= new-tab "credentials"))
                                                       (check-form-validity!))
                                                     (reset! active-tab new-tab))}
                    [:> Tabs.List {:mb "7"}
                     [:> Tabs.Trigger {:value "details"} "Details"]
                     [:> Tabs.Trigger {:value "credentials"} "Credentials"]
                     [:> Tabs.Trigger {:value "terminal"} "Terminal Access"]
                     [:> Tabs.Trigger {:value "native"} "Native Access"]]

                    [:> Tabs.Content {:value "details"}
                     [details-tab/main (:data @connection)]]

                    [:> Tabs.Content {:value "credentials"}
                     [credentials-tab/main (:data @connection)]]

                    [:> Tabs.Content {:value "terminal"}
                     [terminal-access-tab/main (:data @connection)]]

                    [:> Tabs.Content {:value "native"}
                     [native-access-tab/main (:data @connection)]]]]]

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
                                            {:title "Delete role?"
                                             :type :danger
                                             :text-action-button "Confirm and delete"
                                             :action-button? true
                                             :text [:> Box {:class "space-y-radix-4"}
                                                    [:> Text {:as "p"}
                                                     "This action will instantly remove your access to "
                                                     (:name (:data @connection))
                                                     " and can not be undone."]
                                                    [:> Text {:as "p"}
                                                     "Are you sure you want to delete this role?"]]
                                             :on-success (fn []
                                                           (rf/dispatch [:connections->delete-connection (:name (:data @connection))])
                                                           (rf/dispatch [:modal->close]))}])}}]

               ;; Test Connection Modal
               [test-connection-modal/test-connection-modal connection-name]])))}))
    (finally
      (rf/dispatch [:connection-setup/initialize-state nil])
      (rf/dispatch [:connections->clear-connection-details]))))

