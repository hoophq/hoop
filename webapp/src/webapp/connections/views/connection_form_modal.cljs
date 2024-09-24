(ns webapp.connections.views.connection-form-modal

  (:require ["@heroicons/react/16/solid" :as hero-micro-icon]
            ["unique-names-generator" :as ung]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.divider :as divider]
            [webapp.components.forms :as forms]
            [webapp.components.headings :as h]
            [webapp.components.loaders :as loaders]
            [webapp.connections.constants :as constants]
            [webapp.connections.dlp-info-types :as dlp-info-types]
            [webapp.connections.utilities :as utils]
            [webapp.connections.views.form.application :as application]
            [webapp.connections.views.form.custom :as custom]
            [webapp.connections.views.form.database :as database]
            [webapp.connections.views.form.ssh :as ssh]
            [webapp.connections.views.form.tcp :as tcp]
            [webapp.formatters :as f]
            [webapp.subs :as subs]
            ["@heroicons/react/24/solid" :as hero-solid-icon]))


(defn array->select-options [array]
  (mapv #(into {} {"value" % "label" (cs/lower-case (cs/replace % #"_" " "))}) array))

(defn js-select-options->list [options]
  (mapv #(get % "value") options))

(defn random-connection-name []
  (let [numberDictionary (.generate ung/NumberDictionary #js{:length 4})
        characterName (ung/uniqueNamesGenerator #js{:dictionaries #js[ung/animals ung/starWars]
                                                    :style "lowerCase"
                                                    :length 1})]
    (str characterName "-" numberDictionary)))

(defmulti dispatch-form identity)
(defmethod dispatch-form :create
  [_ form-fields]
  (if (empty? (:agent_id form-fields))
    (rf/dispatch [:show-snackbar {:level :error
                                  :text "You cannot create without selecting an agent"}])
    (rf/dispatch [:connections->create-connection form-fields])))
(defmethod dispatch-form :update
  [_ form-fields]
  (if (empty? (:agent_id form-fields))
    (rf/dispatch [:show-snackbar {:level :error
                                  :text "You cannot create without selecting an agent."}])
    (rf/dispatch [:connections->update-connection form-fields])))

(defn add-new-configs
  [config-map config-key config-value]

  (if-not (or (empty? config-key) (empty? config-value))
    (swap! config-map conj {:key config-key :value config-value})
    nil))

(def setup-type-dictionary
  {:database "Database"
   :application "Application"
   :custom "Shell"})

(defn nickname-input [connection-name connection-type form-type]
  [:section {:class "mb-large"}
   [:div {:class "grid grid-cols-1"}
    [h/h4-md
     (str "Setup your " (get setup-type-dictionary @connection-type))]
    [:label {:class "text-xs text-gray-500 my-small"}
     "This name identifies your connection and should be unique"]
    [forms/input {:label "Name"
                  :placeholder (str "my-" @connection-type "-test")
                  :disabled (= form-type :update)
                  :on-change (fn [v]
                               (reset! connection-name (f/replace-empty-space->dash (-> v .-target .-value))))
                  :required true
                  :value @connection-name}]]])

(defn- convertStatusToBool [status]
  (if (= status "enabled")
    true
    false))

(defn form [connection form-type connection-original-type]
  (let [api-key (rf/subscribe [:organization->api-key])
        connections (rf/subscribe [:connections])
        connection-type (r/atom connection-original-type)
        connection-subtype (r/atom (if (empty? (:subtype connection))
                                     (case (:type connection)
                                       "database" "postgres"
                                       "application" "ruby-on-rails"
                                       "custom" "custom"
                                       "" (:type connection)
                                       (:type connection))
                                     (:subtype connection)))
        connection-name (r/atom (or (:name connection) (str (if @connection-subtype
                                                              @connection-subtype
                                                              "custom") "-" (random-connection-name))))
        more-options? (r/atom false)

        data-masking-groups-value (r/atom
                                   (if (= form-type :create)
                                     (array->select-options dlp-info-types/options)
                                     (array->select-options
                                      (:redact_types connection))))
        approval-groups-value (r/atom
                               (if (= form-type :create)
                                 [{"value" "admin" "label" "admin"}]
                                 (array->select-options
                                  (:reviewers connection))))

        agents (rf/subscribe [:agents])
        user-groups (rf/subscribe [:user-groups])
        access-schema-toggle-enabled? (r/atom (or (= (:access_schema connection) "enabled") false))
        access-mode-runbooks (r/atom (if (nil? (:access_mode_runbooks connection))
                                       true
                                       (convertStatusToBool (:access_mode_runbooks connection))))
        access-mode-connect (r/atom (if (nil? (:access_mode_connect connection))
                                      true
                                      (convertStatusToBool (:access_mode_connect connection))))
        access-mode-exec (r/atom (if (nil? (:access_mode_exec connection))
                                   true
                                   (convertStatusToBool (:access_mode_exec connection))))

        review-toggle-enabled? (r/atom (if (and (seq @approval-groups-value)
                                                (= form-type :update))
                                         true
                                         false))
        data-masking-toggle-enabled? (r/atom (if (and (seq @data-masking-groups-value)
                                                      (= form-type :update))
                                               true
                                               false))
        current-agent (if (empty? (:agent_id connection))
                        (first (:data @agents))
                        (first (filter (fn [{:keys [id]}] (= id (:agent_id connection))) (:data @agents))))

        current-agent-id (r/atom (if (empty? current-agent)
                                   ""
                                   (:id current-agent)))
        current-agent-name (r/atom (if (empty? current-agent)
                                     ""
                                     (if (= (cs/upper-case (:status current-agent)) "DISCONNECTED")
                                       (str (:name current-agent) " (" (:status current-agent) ")")
                                       (:name current-agent))))

        connection-command (r/atom (if (empty? (:command connection))
                                     (get constants/connection-commands @connection-subtype)
                                     (cs/join " " (:command connection))))
        configs-file (r/atom (or (utils/json->config
                                  (utils/separate-values-from-config-by-prefix
                                   (:secret connection) "filesystem")) []))
        config-file-name (r/atom "")
        config-file-value (r/atom "")

        config-key (r/atom "")
        config-value (r/atom "")
        configs (r/atom (if (empty? (:secret connection))
                          (utils/get-config-keys (keyword @connection-subtype))
                          (utils/merge-by-key
                           (utils/get-config-keys (keyword @connection-subtype))
                           (utils/json->config
                            (utils/separate-values-from-config-by-prefix (:secret connection) "envvar")))))
        tags-value (r/atom (if (empty? (:tags connection))
                             []
                             (mapv #(into {} {"value" % "label" %}) (:tags connection))))
        tags-input-value (r/atom "")
        create-connection-request
        #(dispatch-form form-type
                        {:name @connection-name
                         :type (name @connection-type)
                         :subtype @connection-subtype
                         :agent_id @current-agent-id
                         :reviewers (if @review-toggle-enabled?
                                      (js-select-options->list @approval-groups-value)
                                      [])
                         :redact_enabled true
                         :redact_types (if @data-masking-toggle-enabled?
                                         (js-select-options->list @data-masking-groups-value)
                                         [])
                         :access_schema (if @access-schema-toggle-enabled?
                                          "enabled"
                                          "disabled")
                         :access_mode_runbooks (if @access-mode-runbooks
                                                 "enabled"
                                                 "disabled")
                         :access_mode_exec (if @access-mode-exec
                                             "enabled"
                                             "disabled")
                         :access_mode_connect (if @access-mode-connect
                                                "enabled"
                                                "disabled")
                         :tags (if (seq @tags-value)
                                 (js-select-options->list @tags-value)
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
                                    (or (re-seq #"'.*?'|\".*?\"|\S+|\t" @connection-command) []))})]
    (rf/dispatch [:users->get-user-groups])
    (rf/dispatch [:users->get-user])
    (rf/dispatch [:plugins->get-my-plugins])
    (rf/dispatch [:organization->get-api-key])
    (fn [_ form-type]
      [:section {:class (str "relative h-full flex flex-col items-center"
                             (when-not @connection-type
                               " justify-center"))}

       (when (= form-type :create)
         [:div {:class (str "absolute top-0 w-full flex justify-end items-center mx-4 my-6 "
                            (when (and (not (seq (:results @connections)))
                                       @connection-type) "justify-between"))}
          [:figure {:class (str "flex gap-2 items-center cursor-pointer "
                                (when (or (seq (:results @connections))
                                          (nil? @connection-type)) "hidden"))
                    :on-click #(reset! connection-type nil)}
           [:> hero-micro-icon/ArrowLeftIcon {:class "h-5 w-5 text-black"
                                              :aria-hidden "true"}]
           [:span {:class "text-black text-sm"}
            "Back"]]

          [:figure {:class "flex gap-2 items-center cursor-pointer"
                    :on-click #(rf/dispatch [:navigate :connections])}
           [:span {:class "text-black text-sm"}
            "Close"]
           [:> hero-micro-icon/XMarkIcon {:class "h-6 w-6 text-black"
                                          :aria-hidden "true"}]]])
       [:form {:class (str "max-w-3xl"
                           (when @connection-type " mt-12"))
               :on-submit (fn [e]
                            (.preventDefault e)
                            (create-connection-request))}
        [:main {:class "my-large"}

         (when (= form-type :create)
           [:div {:class "mb-large"}
            [h/h4 "What do you want to connect to?"
             {:class "text-center mb-large"}]
            [:section {:class "flex  gap-regular justify-center"}
             [:div {:class (str "flex flex-col w-44 items-center gap-small rounded-lg bg-gray-50 hover:shadow "
                                "border border-gray-100 px-1 pt-3 pb-5 cursor-pointer hover:bg-gray-100"
                                (when (= @connection-type :database)
                                  " bg-gray-800 text-white hover:bg-gray-800"))
                    :on-click (fn []
                                (reset! connection-type :database)
                                (reset! connection-subtype "postgres")
                                (reset! access-schema-toggle-enabled? true)
                                (reset! configs (utils/get-config-keys (keyword "postgres")))
                                (reset! connection-name (str "postgres" "-" (random-connection-name)))
                                (reset! connection-command (get constants/connection-commands "postgres")))}
              [:span {:class "text-sm"}
               "Database"]
              [:figure
               [:img {:class "w-full p-3"
                      :src "/images/database-connections-small.svg"}]]]
             [:div {:class (str "flex flex-col w-44 items-center gap-small rounded-lg bg-gray-50 hover:shadow "
                                "border border-gray-100 px-1 pt-3 pb-5 cursor-pointer hover:bg-gray-100"
                                (when (and (= @connection-type :custom)
                                           (not= @connection-subtype "ssh"))
                                  " bg-gray-800 text-white hover:bg-gray-800"))
                    :on-click (fn []
                                (reset! connection-type :custom)
                                (reset! connection-subtype "custom")
                                (reset! access-schema-toggle-enabled? false)
                                (reset! config-file-name "")
                                (reset! connection-name (str "custom" "-" (random-connection-name)))
                                (reset! connection-command "")
                                (reset! configs []))}
              [:span {:class "text-sm"}
               "Shell"]
              [:figure
               [:img {:class "w-full p-3"
                      :src "/images/custom-connections-small.svg"}]]]
             [:div {:class (str "flex flex-col w-44 items-center gap-small rounded-lg bg-gray-50 hover:shadow "
                                "border border-gray-100 px-1 pt-3 pb-5 cursor-pointer hover:bg-gray-100"
                                (when (and (= @connection-type :custom)
                                           (= @connection-subtype "ssh"))
                                  " bg-gray-800 text-white hover:bg-gray-800"))
                    :on-click (fn []
                                (reset! connection-type :custom)
                                (reset! connection-subtype "ssh")
                                (reset! access-schema-toggle-enabled? false)
                                (reset! access-mode-exec false)
                                (reset! access-mode-runbooks false)
                                (reset! config-file-name "SSH_PRIVATE_KEY")
                                (reset! connection-name (str "ssh" "-" (random-connection-name)))
                                (reset! connection-command (get constants/connection-commands "ssh"))
                                (reset! configs (utils/get-config-keys (keyword "ssh"))))}
              [:span {:class "text-sm"}
               "SSH"]
              [:figure
               [:img {:class "w-full p-3"
                      :src "/images/custom-connections-small.svg"}]]]
             [:div {:class (str "flex flex-col w-44 items-center gap-small rounded-lg bg-gray-50 hover:shadow "
                                "border border-gray-100 px-1 pt-3 pb-5 cursor-pointer hover:bg-gray-100"
                                (when (and (= @connection-type :application)
                                           (= @connection-subtype "tcp"))
                                  " bg-gray-800 text-white hover:bg-gray-800"))
                    :on-click (fn []
                                (reset! connection-type :application)
                                (reset! connection-subtype "tcp")
                                (reset! access-schema-toggle-enabled? false)
                                (reset! access-mode-exec false)
                                (reset! access-mode-runbooks false)
                                (reset! connection-name (str "tcp" "-" (random-connection-name)))
                                (reset! configs (utils/get-config-keys (keyword "tcp"))))}
              [:span {:class "text-sm"}
               "TCP"]
              [:figure
               [:img {:class "w-full p-3"
                      :src "/images/tcp-connections-small.svg"}]]]
             [:div {:class (str "flex flex-col w-44 items-center gap-small rounded-lg bg-gray-50 hover:shadow "
                                "border border-gray-100 px-1 pt-3 pb-5 cursor-pointer hover:bg-gray-100"
                                (when (and (= @connection-type :application)
                                           (not= @connection-subtype "tcp"))
                                  " bg-gray-800 text-white hover:bg-gray-800"))
                    :on-click (fn []
                                (reset! connection-subtype "ruby-on-rails")
                                (reset! connection-type :application)
                                (reset! access-schema-toggle-enabled? false)
                                (reset! connection-name (str "ruby-on-rails" "-" (random-connection-name)))
                                (reset! connection-command (get constants/connection-commands "ruby-on-rails"))
                                (reset! configs []))}
              [:span {:class "text-sm"}
               "Application"]
              [:figure
               [:img {:class "w-full p-3"
                      :src "/images/application-connections-small.svg"}]]]]

            (when (and (not (seq (:results @connections)))
                       (not @connection-type))
              [:div {:class "mt-14 flex flex-col items-center"}
               [:span {:class "text-gray-500 text-sm mb-4"}
                "Not sure yet? Try this suggestion"]
               [:div {:class "flex items-center gap-regular border border-gray-100 bg-gray-50 rounded-lg p-4 hover:shadow cursor-pointer"
                      :on-click (fn [] (rf/dispatch [:connections->quickstart-create-postgres-demo]))}
                [:figure
                 [:img {:class "w-16 m-auto"
                        :src "/images/quickstart-connections.svg"}]]
                [:div {:class "flex flex-col justify-center"}
                 [h/h4-md "Quickstart with a Demo PostgreSQL"]
                 [:span {:class "mt-2 text-sm text-center text-gray-500"}
                  "Start with a complete database setup to test all features"]]]])

            (when (and (empty? @current-agent-id)
                       (not @connection-type))
              [:div {:class "mt-52 py-3 px-4 rounded-md flex items-center justify-center bg-gray-100 border border-gray-300"}
               [:span {:class "text-sm text-gray-500"}
                "No Agents found. "
                [:a {:class "text-blue-600 underline"
                     :href "https://hoop.dev/docs/concepts/agent"
                     :target "_blank"}
                 "Click here"]

                " to learn how to setup one before creating a connection."]
               [:> hero-solid-icon/ArrowUpRightIcon {:class "h-4 w-4 text-gray-600 ml-3"}]])])

         (when @connection-type
           [:div
            [nickname-input connection-name connection-type form-type]

            (cond
              (= @connection-type :database)
              [database/main configs {:connection-name connection-name
                                      :connection-type connection-type
                                      :connection-subtype connection-subtype
                                      :connection-command connection-command
                                      :user-groups user-groups
                                      :current-agent-name current-agent-name
                                      :current-agent-id current-agent-id
                                      :tags-value tags-value
                                      :tags-input-value tags-input-value
                                      :form-type form-type
                                      :api-key api-key
                                      :review-toggle-enabled? review-toggle-enabled?
                                      :approval-groups-value approval-groups-value
                                      :data-masking-toggle-enabled? data-masking-toggle-enabled?
                                      :data-masking-groups-value data-masking-groups-value
                                      :access-schema-toggle-enabled? access-schema-toggle-enabled?
                                      :access-mode-runbooks access-mode-runbooks
                                      :access-mode-connect access-mode-connect
                                      :access-mode-exec access-mode-exec}]
              (and (= @connection-type :application)
                   (not= @connection-subtype "tcp"))
              [application/main {:connection-name connection-name
                                 :connection-type connection-type
                                 :connection-subtype connection-subtype
                                 :connection-command connection-command
                                 :tags-value tags-value
                                 :tags-input-value tags-input-value
                                 :user-groups user-groups
                                 :current-agent-name current-agent-name
                                 :current-agent-id current-agent-id
                                 :form-type form-type
                                 :api-key api-key
                                 :review-toggle-enabled? review-toggle-enabled?
                                 :approval-groups-value approval-groups-value
                                 :data-masking-toggle-enabled? data-masking-toggle-enabled?
                                 :data-masking-groups-value data-masking-groups-value
                                 :access-mode-runbooks access-mode-runbooks
                                 :access-mode-connect access-mode-connect
                                 :access-mode-exec access-mode-exec}]
              (and (= @connection-type :application)
                   (= @connection-subtype "tcp"))
              [tcp/main configs {:user-groups user-groups
                                 :current-agent-name current-agent-name
                                 :current-agent-id current-agent-id
                                 :tags-value tags-value
                                 :tags-input-value tags-input-value
                                 :form-type form-type
                                 :review-toggle-enabled? review-toggle-enabled?
                                 :approval-groups-value approval-groups-value
                                 :data-masking-toggle-enabled? data-masking-toggle-enabled?
                                 :data-masking-groups-value data-masking-groups-value
                                 :access-mode-runbooks access-mode-runbooks
                                 :access-mode-connect access-mode-connect
                                 :access-mode-exec access-mode-exec}]
              (and (= @connection-type :custom)
                   (not= @connection-subtype "ssh"))
              [custom/main configs configs-file {:connection-name connection-name
                                                 :connection-type connection-type
                                                 :connection-subtype connection-subtype
                                                 :connection-command connection-command
                                                 :tags-value tags-value
                                                 :tags-input-value tags-input-value
                                                 :user-groups user-groups
                                                 :current-agent-name current-agent-name
                                                 :current-agent-id current-agent-id
                                                 :config-file-name config-file-name
                                                 :config-file-value config-file-value
                                                 :config-key config-key
                                                 :config-value config-value
                                                 :form-type form-type
                                                 :api-key api-key
                                                 :review-toggle-enabled? review-toggle-enabled?
                                                 :approval-groups-value approval-groups-value
                                                 :data-masking-toggle-enabled? data-masking-toggle-enabled?
                                                 :data-masking-groups-value data-masking-groups-value
                                                 :access-mode-runbooks access-mode-runbooks
                                                 :access-mode-connect access-mode-connect
                                                 :access-mode-exec access-mode-exec
                                                 :on-click->add-more #(do
                                                                        (add-new-configs configs @config-key @config-value)
                                                                        (reset! config-value "")
                                                                        (reset! config-key ""))
                                                 :on-click->add-more-file #(do
                                                                             (add-new-configs configs-file @config-file-name @config-file-value)
                                                                             (reset! config-file-name "")
                                                                             (reset! config-file-value ""))}]
              (and (= @connection-type :custom)
                   (= @connection-subtype "ssh"))
              [ssh/main configs configs-file {:tags-value tags-value
                                              :tags-input-value tags-input-value
                                              :user-groups user-groups
                                              :current-agent-name current-agent-name
                                              :current-agent-id current-agent-id
                                              :config-file-value config-file-value
                                              :form-type form-type
                                              :review-toggle-enabled? review-toggle-enabled?
                                              :approval-groups-value approval-groups-value
                                              :data-masking-toggle-enabled? data-masking-toggle-enabled?
                                              :data-masking-groups-value data-masking-groups-value
                                              :access-mode-runbooks access-mode-runbooks
                                              :access-mode-connect access-mode-connect
                                              :access-mode-exec access-mode-exec}])])
         (when (= form-type :update)
           [:section
            [divider/main]

            [h/h4-md "Danger Zone"
             {:class "mt-large mb-regular"}]
            [:div {:class "flex items-center justify-between p-4 border border-red-200 rounded-md"}
             [:div {:class "flex flex-col gap-1"}
              [:span {:class "text-sm text-gray-700 font-bold"}
               "Delete connection"]
              [:span {:class "text-xs text-gray-500"}
               "Once you delete a connection, this action cannot be undone."]]
             [button/red-new {:text "Delete connection"
                              :size :small
                              :variant :outline
                              :on-click #(rf/dispatch [:dialog->open {:title "Delete connection"
                                                                      :type :danger
                                                                      :text "Are you sure you want to delete your connection? This action cannot be undone."
                                                                      :on-success (fn []
                                                                                    (reset! more-options? false)
                                                                                    (rf/dispatch [:connections->delete-connection @connection-name])
                                                                                    (rf/dispatch [:close-modal]))}])}]]])]]])))

(defn- loading-list-view []
  [:div {:class "flex items-center justify-center h-full"}
   [loaders/simple-loader]])

(defn main [form-type data connection-original-type]
  (let [connection (rf/subscribe [::subs/connections->updating-connection])
        agents (rf/subscribe [:agents])]
    (rf/dispatch [:agents->get-agents])
    (rf/dispatch [:connections->get-connections])
    (fn []
      (case form-type
        :create (if (= :ready (:status @agents))
                  [form data :create connection-original-type]
                  [loading-list-view])

        :update (if (or (= :loading (:status @agents)) (true? (:loading @connection)))
                  [loading-list-view]
                  [form (:data @connection) :update (case (:type (:data @connection))
                                                      ("command-line" "custom") :custom
                                                      ("ruby-on-rails" "application" "nodejs" "clojure" "python") :application
                                                      ("mysql" "mssql" "postgres" "mongodb" "database") :database)])))))
