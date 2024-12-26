(ns webapp.connections.views.create-update-connection.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Link Strong Text]]
   ["lucide-react" :refer [ArrowLeft BadgeInfo GlobeLock ShieldEllipsis
                           SquareStack]]
   [clojure.string :as s]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.accordion :as accordion]
   [webapp.connections.constants :as constants]
   [webapp.connections.dlp-info-types :as ai-data-masking-info-types]
   [webapp.connections.helpers :as helpers]
   [webapp.connections.views.create-update-connection.connection-advance-settings-form :as connection-advance-settings-form]
   [webapp.connections.views.create-update-connection.connection-details-form :as connection-details-form]
   [webapp.connections.views.create-update-connection.connection-environment-form :as connection-environment-form]
   [webapp.connections.views.create-update-connection.connection-type-form :as connection-type-form]))

(defn- convertStatusToBool [status]
  (if (= status "enabled")
    true
    false))

(defn add-new-configs
  [config-map config-key config-value]

  (if-not (or (empty? config-key) (empty? config-value))
    (swap! config-map conj {:key config-key :value config-value})
    nil))

(defn transform-filtered-guardrails-selected [guardrails connection-guardrail-ids]
  (->> guardrails
       (filter #(some #{(:id %)} connection-guardrail-ids))
       (mapv (fn [{:keys [id name]}]
               {:value id
                :label name}))))

(defn transform-filtered-jira-template-selected [jira-templates jira-template-id]
  (first
   (->> jira-templates
        (filter #(= (:id %) jira-template-id))
        (mapv (fn [{:keys [id name]}]
                {"value" id
                 "label" name})))))

(defmulti dispatch-form identity)
(defmethod dispatch-form :create
  [_ form-fields]
  (rf/dispatch [:connections->create-connection form-fields]))
(defmethod dispatch-form :update
  [_ form-fields]
  (rf/dispatch [:connections->update-connection form-fields]))

(defn verify-form-accordion [configs id]
  (doseq [item configs]
    (when (and (s/blank? (:value item))
               (:required item))
      (.click
       (.querySelector
        (-> js/window .-document)
        (str "#" id " button[data-state=\"closed\"]"))))))

(defn select-header-by-form-type [form-type connection]
  (case form-type
    :create [:> Box
             [:> Heading {:size "8" :as "h1" :class "text-gray-12"} "Create Connection"]
             [:> Text {:size "5" :class "text-gray-11"} "Setup a secure access to your resources."]]
    :update [:> Box {:class "space-y-radix-2"}
             [:> Heading {:size "8" :as "h1" :class "text-gray-12"} "Configure"]
             [:> Flex {:gap "3" :align "center"}
              [:> Box
               [:figure {:class "w-6"}
                [:img {:src (constants/get-connection-icon connection)}]]]
              [:> Text {:size "5" :class "text-gray-11"}
               (:name connection)]]]))

(defn main [form-type connection]
  (let [agents (rf/subscribe [:agents])
        api-key (rf/subscribe [:organization->api-key])
        user (rf/subscribe [:users->current-user])
        user-groups (rf/subscribe [:user-groups])
        guardrails-list (rf/subscribe [:guardrails->list])
        jira-templates-list (rf/subscribe [:jira-templates->list])

        scroll-pos (r/atom 0)
        accordion-resource-type (r/atom true)
        accordion-connection-details (r/atom false)
        accordion-environment-setup (r/atom false)
        accordion-advanced-settings (r/atom false)

        agent-id (r/atom (or (:agent_id connection) ""))
        connection-type (r/atom (or (:type connection) nil))
        connection-subtype (r/atom (or (:subtype connection) nil))
        connection-name (r/atom (or (:name connection) ""))
        connection-tags-value (r/atom (if (empty? (:tags connection))
                                        []
                                        (mapv #(into {} {"value" % "label" %}) (:tags connection))))
        connection-tags-input-value (r/atom "")
        review-groups (r/atom (if (= form-type :create)
                                [{"value" "admin" "label" "admin"}]
                                (helpers/array->select-options
                                 (:reviewers connection))))
        enable-review? (r/atom (if (and (seq @review-groups)
                                        (= form-type :update))
                                 true
                                 false))
        ai-data-masking-info-types (r/atom
                                    (if (= form-type :create)
                                      (helpers/array->select-options ai-data-masking-info-types/options)
                                      (helpers/array->select-options
                                       (:redact_types connection))))
        ai-data-masking (r/atom (if (and (:redact_enabled connection)
                                         (= form-type :update))
                                  true
                                  false))
        guardrails (r/atom (if (empty? (:guardrail_rules connection))
                             []
                             (transform-filtered-guardrails-selected
                              (:data @guardrails-list)
                              (:guardrail_rules connection))))

        jira-template-id (r/atom (if (:jira_issue_template_id connection)
                                   (transform-filtered-jira-template-selected
                                    (:data @jira-templates-list)
                                    (:jira_issue_template_id connection))
                                   ""))

        database-schema? (r/atom (or (convertStatusToBool (:access_schema connection)) false))
        access-mode-runbooks? (r/atom (if (nil? (:access_mode_runbooks connection))
                                        true
                                        (convertStatusToBool (:access_mode_runbooks connection))))
        access-mode-exec? (r/atom (if (nil? (:access_mode_exec connection))
                                    true
                                    (convertStatusToBool (:access_mode_exec connection))))
        access-mode-connect? (r/atom (if (nil? (:access_mode_connect connection))
                                       true
                                       (convertStatusToBool (:access_mode_connect connection))))
        configs (r/atom (if (empty? (:secret connection))
                          (helpers/get-config-keys (keyword @connection-subtype))
                          (helpers/merge-by-key
                           (helpers/get-config-keys (keyword @connection-subtype))
                           (helpers/json->config
                            (helpers/separate-values-from-config-by-prefix (:secret connection) "envvar")))))
        config-key (r/atom "")
        config-value (r/atom "")
        configs-file (r/atom (or (helpers/json->config
                                  (helpers/separate-values-from-config-by-prefix
                                   (:secret connection) "filesystem")) []))
        config-file-name (r/atom "")
        config-file-value (r/atom "")
        connection-command (r/atom (if (empty? (:command connection))
                                     (get constants/connection-commands @connection-subtype)
                                     (s/join " " (:command connection))))]
    (rf/dispatch [:agents->get-agents])
    (rf/dispatch [:users->get-user-groups])
    (rf/dispatch [:users->get-user])
    (rf/dispatch [:organization->get-api-key])
    (rf/dispatch [:guardrails->get-all])
    (rf/dispatch [:jira-templates->get-all])
    (fn []
      (let [first-step-finished (boolean (or @connection-type
                                             (= form-type :update)))
            second-step-finished (boolean (and first-step-finished
                                               (not (s/blank? @connection-name))))
            third-step-finished (boolean (and second-step-finished
                                              (not (s/blank? @connection-command))))
            free-license? (-> @user :data :free-license?)]

        (r/with-let [handle-scroll (fn []
                                     (reset! scroll-pos (.-scrollY js/window)))
                     _ (add-watch connection-type :first-step-watcher
                                  (fn [_ _ old-val new-val]
                                    (when (and new-val (not old-val))
                                      (reset! accordion-connection-details true)
                                      (reset! accordion-environment-setup true))))
                     _ (add-watch jira-templates-list :jira-template-watcher
                                  (fn [_ _ old-val new-val]
                                    (when (and (= :ready (:status new-val))
                                               (:jira_issue_template_id connection))
                                      (reset! jira-template-id
                                              (transform-filtered-jira-template-selected
                                               (:data new-val)
                                               (:jira_issue_template_id connection))))))]
          (.addEventListener js/window "scroll" handle-scroll)

          [:> Box {:class "min-h-screen bg-gray-1"}
           [:form {:id "connection-form"
                   :on-submit (fn [e]
                                (.preventDefault e)
                                (dispatch-form
                                 form-type
                                 {:name @connection-name
                                  :type (when @connection-type
                                          @connection-type)
                                  :subtype @connection-subtype
                                  :agent_id @agent-id
                                  :reviewers (if @enable-review?
                                               (helpers/js-select-options->list @review-groups)
                                               [])
                                  :redact_enabled true
                                  :redact_types (if @ai-data-masking
                                                  (helpers/js-select-options->list @ai-data-masking-info-types)
                                                  [])
                                  :access_schema (if @database-schema?
                                                   "enabled"
                                                   "disabled")
                                  :access_mode_runbooks (if @access-mode-runbooks?
                                                          "enabled"
                                                          "disabled")
                                  :access_mode_exec (if @access-mode-exec?
                                                      "enabled"
                                                      "disabled")
                                  :access_mode_connect (if @access-mode-connect?
                                                         "enabled"
                                                         "disabled")
                                  :guardrail_rules (if (seq @guardrails)
                                                     (helpers/js-select-options->list @guardrails)
                                                     [])
                                  :jira_issue_template_id (get @jira-template-id "value")
                                  :tags (if (seq @connection-tags-value)
                                          (helpers/js-select-options->list @connection-tags-value)
                                          nil)
                                  :secret (clj->js
                                           (merge
                                            (helpers/config->json (conj
                                                                   @configs
                                                                   {:key @config-key
                                                                    :value @config-value})
                                                                  "envvar:")
                                            (when (and @config-file-value @config-file-name)
                                              (helpers/config->json
                                               (conj
                                                @configs-file
                                                {:key @config-file-name
                                                 :value @config-file-value})
                                               "filesystem:"))))

                                  :command (if (= @connection-type "database")
                                             []
                                             (when @connection-command
                                               (or (re-seq #"'.*?'|\".*?\"|\S+|\t" @connection-command) [])))}))}

            [:<>
             (when (= form-type :create)
               [:> Flex {:p "5" :gap "2"}
                [:> Button {:variant "ghost"
                            :size "2"
                            :color "gray"
                            :type "button"
                            :on-click #(js/history.back)}
                 [:> ArrowLeft {:size 16}]
                 "Back"]])
             [:> Box {:pt "5"
                      :px "7"
                      :class (str "sticky top-0 z-50 bg-gray-1 "
                                  (if (= form-type :create)
                                    " bg-gray-1 "
                                    " bg-white border-b border-[--gray-a6] pb-[--space-5] top-0 z-50 -m-10 mb-0 p-10 ")
                                  (if (>= @scroll-pos 30)
                                    " border-b border-[--gray-a6] pb-[--space-5]"
                                    " "))}
              [:> Flex {:justify "between"
                        :align "center"}
               [select-header-by-form-type form-type {:name @connection-name
                                                      :type @connection-type
                                                      :subtype @connection-subtype
                                                      :icon_name ""}]
               [:> Flex {:gap "5" :align "center"}
                (when (= form-type :update)
                  [:> Button {:size "4"
                              :variant "ghost"
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
                                                                [:> Strong
                                                                 @connection-name]
                                                                " and can not be undone."]
                                                               [:> Text {:as "p"}
                                                                "Are you sure you want to delete this connection?"]]
                                                        :on-success (fn []
                                                                      (rf/dispatch [:connections->delete-connection @connection-name])
                                                                      (rf/dispatch [:modal->close]))}])}
                   "Delete"])
                (if (and (= "application" @connection-type)
                         (not= "tcp" @connection-subtype))
                  [:> Text {:size "3" :align "right" :class "text-gray-11"}
                   "If you have finished the setup"
                   [:br]
                   [:> Link {:size "3"
                             :href "#"
                             :on-click (fn []
                                         (rf/dispatch [:connections->get-connections])
                                         (rf/dispatch [:navigate :connections]))}
                    "check your connections."]]

                  [:> Button {:size "4"
                              :disabled (not first-step-finished)
                              :on-click (fn []
                                          (let [form (.getElementById (-> js/window .-document) "connection-form")]
                                            (verify-form-accordion (conj @configs {:value @agent-id
                                                                                   :required true}) "environment-setup")
                                            (verify-form-accordion [{:value @connection-name
                                                                     :required true}] "connection-details")

                                            (when (or (not (.checkValidity form))
                                                      (not @agent-id))
                                              (.reportValidity form)
                                              false)))}
                   (if (= form-type :create)
                     "Save and Confirm"
                     "Save")])]]]]

            [:> Box {:p "7" :class "space-y-radix-5"}
             (when (= form-type :create)
               [accordion/root
                {:item {:title "Set your resource type"
                        :subtitle "Connections can be created for databases, applications and more."
                        :value "resource-type"
                        :show-icon? first-step-finished
                        :avatar-icon [:> SquareStack {:size 16}]
                        :content [connection-type-form/main
                                  {:connection-type connection-type
                                   :connection-subtype connection-subtype
                                   :connection-name connection-name
                                   :configs configs
                                   :config-file-name config-file-name
                                   :database-schema? database-schema?
                                   :connection-command connection-command}]}
                 :id "resource-type"
                 :open? @accordion-resource-type
                 :on-change #(reset! accordion-resource-type %)}])

             [accordion/root
              {:item {:title "Define connection details"
                      :subtitle "Setup how do you want to identify the connection and core configuration parameters."
                      :value "connection-details"
                      :show-icon? second-step-finished
                      :disabled (not first-step-finished)
                      :avatar-icon [:> BadgeInfo {:size 16}]
                      :content [connection-details-form/main
                                {:user-groups user-groups
                                 :free-license? free-license?
                                 :connection-subtype connection-subtype
                                 :connection-name connection-name
                                 :form-type form-type
                                 :reviews enable-review?
                                 :review-groups review-groups
                                 :ai-data-masking ai-data-masking
                                 :ai-data-masking-info-types ai-data-masking-info-types}]}
               :id "connection-details"
               :on-change #(reset! accordion-connection-details %)
               :open? @accordion-connection-details}]

             [accordion/root
              {:item {:title "Environment setup"
                      :subtitle "Setup your environment information to establish a secure connection."
                      :value "environment-setup"
                      :avatar-icon [:> GlobeLock {:size 16}]
                      :show-icon? third-step-finished
                      :disabled (not second-step-finished)
                      :content [connection-environment-form/main
                                {:agents @agents
                                 :agent-id agent-id
                                 :api-key api-key
                                 :connection-name connection-name
                                 :connection-type connection-type
                                 :connection-subtype connection-subtype
                                 :configs configs
                                 :config-key config-key
                                 :config-value config-value
                                 :configs-file configs-file
                                 :config-file-name config-file-name
                                 :config-file-value config-file-value
                                 :connection-command connection-command
                                 :reviews enable-review?
                                 :review-groups review-groups
                                 :ai-data-masking ai-data-masking
                                 :ai-data-masking-info-types ai-data-masking-info-types
                                 :on-click->add-more-key-value #(do
                                                                  (add-new-configs configs @config-key @config-value)
                                                                  (reset! config-value "")
                                                                  (reset! config-key ""))
                                 :on-click->add-more-file-content #(do
                                                                     (add-new-configs configs-file @config-file-name @config-file-value)
                                                                     (reset! config-file-name "")
                                                                     (reset! config-file-value ""))}]}
               :id "environment-setup"
               :on-change #(reset! accordion-environment-setup %)
               :open? @accordion-environment-setup}]
             [accordion/root
              {:item {:title "Advanced settings"
                      :subtitle "Include additional configuration parameters."
                      :value "advanced-settings"
                      :avatar-icon [:> ShieldEllipsis {:size 16}]
                      :show-icon? third-step-finished
                      :disabled (not second-step-finished)
                      :content [connection-advance-settings-form/main
                                {:connection-type connection-type
                                 :connection-subtype connection-subtype
                                 :connection-tags-value connection-tags-value
                                 :connection-tags-input-value connection-tags-input-value
                                 :enable-database-schema database-schema?
                                 :access-mode-runbooks access-mode-runbooks?
                                 :access-mode-exec access-mode-exec?
                                 :access-mode-connect access-mode-connect?
                                 :guardrails-options (or (mapv #(into {} {"value" (:id %) "label" (:name %)})
                                                               (-> @guardrails-list :data)) [])
                                 :guardrails guardrails
                                 :jira-templates-options (or (mapv #(into {} {"value" (:id %) "label" (:name %)})
                                                                   (-> @jira-templates-list :data)) [])
                                 :jira-template-id jira-template-id}]}
               :id "advanced-settings"
               :open? @accordion-advanced-settings
               :on-change #(reset! accordion-advanced-settings %)}]]]]

          (finally
            (remove-watch connection-type :first-step-watcher)
            (.removeEventListener js/window "scroll" handle-scroll)
            (remove-watch jira-templates-list :jira-template-watcher)))))))
