(ns webapp.connections.views.create-update-connection.main
  (:require ["@radix-ui/themes" :refer [Box Button Flex Heading Strong Text]]
            ["lucide-react" :refer [BadgeInfo GlobeLock SquareStack]]
            [clojure.string :as s]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.accordion :as accordion]
            [webapp.connections.constants :as constants]
            [webapp.connections.dlp-info-types :as ai-data-masking-info-types]
            [webapp.connections.utilities :as utils]
            [webapp.connections.views.create-update-connection.connection-details-form :as connection-details-form]
            [webapp.connections.views.create-update-connection.connection-environment-form :as connection-environment-form]
            [webapp.connections.views.create-update-connection.connection-type-form :as connection-type-form]))

(defn js-select-options->list [options]
  (mapv #(get % "value") options))

(defn array->select-options [array]
  (mapv #(into {} {"value" % "label" (s/lower-case (s/replace % #"_" " "))}) array))

(defn- convertStatusToBool [status]
  (if (= status "enabled")
    true
    false))

(defn add-new-configs
  [config-map config-key config-value]

  (println "config-map" config-map)

  (if-not (or (empty? config-key) (empty? config-value))
    (swap! config-map conj {:key config-key :value config-value})
    nil))

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
             [:> Heading {:size "8" :as "h1"} "Create Connection"]
             [:> Text {:size "5"} "Setup a secure access to your resources."]]
    :update [:> Box {:class "space-y-radix-2"}
             [:> Heading {:size "8" :as "h1"} "Configure"]
             [:> Flex {:gap "3" :align "center"}
              [:> Box
               [:figure {:class "w-6"}
                [:img {:src (constants/get-connection-icon connection)}]]]
              [:> Text {:size "5"}
               (:name connection)]]]))

(defn main [form-type connection]
  (let [agents (rf/subscribe [:agents])
        api-key (rf/subscribe [:organization->api-key])
        user (rf/subscribe [:users->current-user])
        user-groups (rf/subscribe [:user-groups])

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
                                (array->select-options
                                 (:reviewers connection))))
        enable-review? (r/atom (if (and (seq @review-groups)
                                        (= form-type :update))
                                 true
                                 false))
        ai-data-masking-info-types (r/atom
                                    (if (= form-type :create)
                                      (array->select-options ai-data-masking-info-types/options)
                                      (array->select-options
                                       (:redact_types connection))))
        ai-data-masking (r/atom (if (and (:redact_enabled connection)
                                         (= form-type :update))
                                  true
                                  false))
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
                          (utils/get-config-keys (keyword @connection-subtype))
                          (utils/merge-by-key
                           (utils/get-config-keys (keyword @connection-subtype))
                           (utils/json->config
                            (utils/separate-values-from-config-by-prefix (:secret connection) "envvar")))))
        config-key (r/atom "")
        config-value (r/atom "")
        configs-file (r/atom (or (utils/json->config
                                  (utils/separate-values-from-config-by-prefix
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
    (fn []
      (let [first-step-finished (boolean (or @connection-type
                                             (= form-type :update)))
            second-step-finished (boolean (and first-step-finished
                                               (not (s/blank? @connection-name))))
            third-step-finished (boolean (and second-step-finished
                                              (not (s/blank? @connection-command))))
            free-license? (-> @user :data :free-license?)]
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
                                            (js-select-options->list @review-groups)
                                            [])
                               :redact_enabled true
                               :redact_types (if @ai-data-masking
                                               (js-select-options->list @ai-data-masking-info-types)
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
                               :tags (if (seq @connection-tags-value)
                                       (js-select-options->list @connection-tags-value)
                                       nil)
                               :secret (clj->js
                                        (merge
                                         (utils/config->json (conj
                                                              @configs
                                                              {:key @config-key
                                                               :value @config-value})
                                                             "envvar:")
                                         (when (and @config-file-value @config-file-name)
                                           (utils/config->json
                                            (conj
                                             @configs-file
                                             {:key @config-file-name
                                              :value @config-file-value})
                                            "filesystem:"))))

                               :command (when @connection-command
                                          (or (re-seq #"'.*?'|\".*?\"|\S+|\t" @connection-command) []))}))}

         [:> Flex {:direction "column" :gap "5"}
          [:> Flex {:justify "between" :py "5" :mb "7" :class "sticky top-0 bg-white z-10"}
           [select-header-by-form-type form-type {:name @connection-name
                                                  :type @connection-type
                                                  :subtype @connection-subtype
                                                  :icon_name ""}]
           [:> Flex {:gap "6" :align "center"}
            (when (= form-type :update)
              [:> Button {:size "4"
                          :variant "ghost"
                          :color "red"
                          :type "button"
                          :on-click #(rf/dispatch [:dialog->open
                                                   {:title "Delete connection?"
                                                    :type :danger
                                                    :text-action-button "Confirm and delete"
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
            (when-not (and (= "application" @connection-type)
                           (not= "tcp" @connection-subtype))
              [:> Button {:size "4"
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
                 "Save")])]]

          [:> Box {:class "space-y-radix-5"}
           (when (= form-type :create)
             [accordion/main [{:title "Choose your resource type"
                               :subtitle "Connections can be created for databases, applications and more."
                               :value "resource-type"
                               :show-icon? first-step-finished
                               :avatar-icon [:> SquareStack {:size 16}]
                               :content [connection-type-form/main connection-type connection-subtype configs config-file-name]}]])

           [accordion/main [{:title "Define connection details"
                             :subtitle "Setup how do you want to identify the connection and additional configuration parameters."
                             :value "connection-details"
                             :show-icon? second-step-finished
                             :disabled (not first-step-finished)
                             :avatar-icon [:> BadgeInfo {:size 16}]
                             :content [connection-details-form/main
                                       {:user-groups user-groups
                                        :free-license? free-license?
                                        :connection-name connection-name
                                        :connection-type connection-type
                                        :connection-subtype connection-subtype
                                        :connection-tags-value connection-tags-value
                                        :connection-tags-input-value connection-tags-input-value
                                        :form-type form-type
                                        :reviews enable-review?
                                        :review-groups review-groups
                                        :ai-data-masking ai-data-masking
                                        :ai-data-masking-info-types ai-data-masking-info-types
                                        :enable-database-schema database-schema?
                                        :access-mode-runbooks access-mode-runbooks?
                                        :access-mode-exec access-mode-exec?
                                        :access-mode-connect access-mode-connect?}]}]
            "connection-details"]

           [accordion/main
            [{:title "Environment setup"
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
                                                             (reset! config-file-value ""))}]}]
            "environment-setup"]]]]))))
