(ns webapp.resources.configure-role.main
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
   [webapp.resources.configure-role.credentials-tab :as credentials-tab]
   [webapp.resources.configure-role.details-tab :as details-tab]
   [webapp.resources.configure-role.native-access-tab :as native-access-tab]
   [webapp.resources.configure-role.terminal-access-tab :as terminal-access-tab]))

(defn get-form-id
  "Returns the form ID based on connection type"
  [connection-type connection-subtype]
  (if (and (= connection-type "application")
           (= connection-subtype "ssh"))
    "ssh-credentials-form"
    "credentials-form"))

(defn get-query-param
  "Gets a query parameter from URL"
  [param-name]
  (-> (js/URLSearchParams. (.. js/window -location -search))
      (.get param-name)))

(defn header [{:keys [name type subtype]} test-connection-state]
  [:> Box {:class "pb-[--space-5]"}
   [:> Flex {:justify "between" :align "center"}
    [:> Box {:class "space-y-radix-3"}
     [:> Heading {:size "6" :weight "bold" :class "text-[--gray-12]"} "Configure"]
     [:> Flex {:gap "3" :align "center"}
      [:figure {:class "w-4"}
       [:img {:src (constants/get-connection-icon {:type type :subtype subtype})}]]
      [:> Text {:size "3" :class "text-[--gray-12]"} name]]]

    (when (can-test-connection? {:type type :subtype subtype})
      [:> Button {:variant "soft"
                  :color "gray"
                  :on-click #(rf/dispatch [:connections->test-connection name])
                  :disabled (is-connection-testing? test-connection-state name)}
       "Test Connection"])]])

(defn loading-view []
  [:div.flex.items-center.justify-center.rounded-lg.border.bg-white.h-full
   [:div.flex.items-center.justify-center.h-full
    [loaders/simple-loader]]])

(defn main [connection-name]
  (r/with-let
    [connection (rf/subscribe [:connections->connection-details])
     guardrails-list (rf/subscribe [:guardrails->list])
     jira-templates-list (rf/subscribe [:jira-templates->list])
     test-connection-state (rf/subscribe [:connections->test-connection])

     ;; Local state
     active-tab (r/atom "details")
     from-page (r/atom (get-query-param "from_page"))
     initialized? (r/atom false)

     ;; Initialize data
     _ (rf/dispatch-sync [:connections->get-connection-details connection-name])
     _ (rf/dispatch [:guardrails->get-all])
     _ (rf/dispatch [:jira-templates->get-all])]

    (fn []
      (let [conn-data (:data @connection)
            loading? (or (:loading @connection)
                         (= (:status @guardrails-list) :loading)
                         (= (:status @jira-templates-list) :loading))

            ;; Submit handler
            handle-save (fn []
                          (let [{:keys [type subtype]} conn-data
                                form-id (get-form-id type subtype)
                                form (.getElementById js/document form-id)]

                            ;; Se form não existe, vai para credentials tab
                            (if-not form
                              (reset! active-tab "credentials")

                              ;; Valida e submete
                              (if (.checkValidity form)
                                (do
                                  ;; SSH: limpar campos não usados
                                  (when (and (= type "application") (= subtype "ssh"))
                                    (let [auth-method @(rf/subscribe [:connection-setup/ssh-auth-method])]
                                      (case auth-method
                                        "password" (rf/dispatch [:connection-setup/update-ssh-credentials "authorized_server_keys" ""])
                                        "key" (rf/dispatch [:connection-setup/update-ssh-credentials "pass" ""])
                                        nil)))

                                  ;; Processar tags pendentes
                                  (when-let [tag-key @(rf/subscribe [:connection-setup/current-key])]
                                    (when (.-value tag-key)
                                      (let [tag-value @(rf/subscribe [:connection-setup/current-value])]
                                        (rf/dispatch [:connection-setup/add-tag
                                                      (.-value tag-key)
                                                      (if tag-value (.-value tag-value) "")]))))

                                  ;; Submit
                                  (rf/dispatch [:resources->update-role-connection
                                                {:name connection-name
                                                 :resource-name (:resource_name conn-data)
                                                 :from-page @from-page}]))

                                ;; Form inválido
                                (do
                                  (reset! active-tab "credentials")
                                  (js/setTimeout #(.reportValidity form) 200))))))]

        (when (and (not loading?)
                   conn-data
                   (not @initialized?))
          (let [processed (helpers/process-connection-for-update
                           conn-data
                           (:data @guardrails-list)
                           (:data @jira-templates-list))]
            (rf/dispatch [:connection-setup/initialize-state processed])
            (reset! initialized? true)))

        (if loading?
          [loading-view]

          (when conn-data
            [:div
             [page-wrapper/main
              {:children
               [:> Box {:class "bg-gray-1 min-h-screen px-4 py-10 sm:px-6 lg:px-20 lg:pt-6 lg:pb-10"}
                [header conn-data @test-connection-state]

                [:> Tabs.Root {:value @active-tab
                               :on-value-change #(reset! active-tab %)}
                 [:> Tabs.List {:mb "7"}
                  [:> Tabs.Trigger {:value "details"} "Details"]
                  [:> Tabs.Trigger {:value "credentials"} "Credentials"]
                  [:> Tabs.Trigger {:value "terminal"} "Terminal Access"]
                  [:> Tabs.Trigger {:value "native"} "Native Access"]]

                 ;; forceMount + hidden mantido para garantir que forms sejam renderizados
                 [:> Tabs.Content {:value "details" :force-mount true}
                  [:> Box {:class (when (not= @active-tab "details") "hidden")}
                   [details-tab/main conn-data]]]

                 [:> Tabs.Content {:value "credentials" :force-mount true}
                  [:> Box {:class (when (not= @active-tab "credentials") "hidden")}
                   [credentials-tab/main conn-data]]]

                 [:> Tabs.Content {:value "terminal" :force-mount true}
                  [:> Box {:class (when (not= @active-tab "terminal") "hidden")}
                   [terminal-access-tab/main conn-data]]]

                 [:> Tabs.Content {:value "native" :force-mount true}
                  [:> Box {:class (when (not= @active-tab "native") "hidden")}
                   [native-access-tab/main conn-data]]]]]

               :footer-props
               {:form-type :update
                :back-text "Back"
                :next-text "Save"
                :on-back #(js/history.back)
                :on-next handle-save
                :on-delete #(rf/dispatch [:dialog->open
                                          {:title "Delete role?"
                                           :type :danger
                                           :text-action-button "Confirm and delete"
                                           :action-button? true
                                           :text [:> Box {:class "space-y-radix-4"}
                                                  [:> Text {:as "p"}
                                                   "This action will instantly remove your access to "
                                                   (:name conn-data)
                                                   " and can not be undone."]
                                                  [:> Text {:as "p"}
                                                   "Are you sure you want to delete this role?"]]
                                           :on-success (fn []
                                                         (rf/dispatch [:connections->delete-connection (:name conn-data)])
                                                         (rf/dispatch [:modal->close]))}])}}]

             [test-connection-modal/test-connection-modal connection-name]]))))

    (finally
      (rf/dispatch [:connection-setup/initialize-state nil])
      (rf/dispatch [:connections->clear-connection-details]))))
