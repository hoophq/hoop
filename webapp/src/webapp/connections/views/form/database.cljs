(ns webapp.connections.views.form.database
  (:require ["@headlessui/react" :as ui]
            ["unique-names-generator" :as ung]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.checkboxes :as checkboxes]
            [webapp.components.headings :as h]
            [webapp.components.multiselect :as multi-select]
            [webapp.components.tabs :as tabs]
            [webapp.components.toggle :as toggle]
            [webapp.connections.constants :as constants]
            [webapp.connections.utilities :as utils]
            [webapp.connections.views.configuration-inputs :as config-inputs]
            [webapp.connections.views.form.hoop-run-instructions :as instructions]
            [webapp.connections.views.form.submit :as submit]
            [webapp.connections.views.form.toggle-data-masking :as toggle-data-masking]
            [webapp.connections.views.form.toggle-review :as toggle-review]
            [webapp.shared-ui.sidebar.connection-overlay :as connection-overlay]))

(defn random-connection-name []
  (let [numberDictionary (.generate ung/NumberDictionary #js{:length 4})
        characterName (ung/uniqueNamesGenerator #js{:dictionaries #js[ung/animals ung/starWars]
                                                    :style "lowerCase"
                                                    :length 1})]
    (str characterName "-" numberDictionary)))

(defn js-select-options->list [options]
  (mapv #(get % "value") options))

(defn manual-credentials-view
  [configs
   agents
   {:keys [current-agent-name
           current-agent-id
           form-type]}]
  [:<>
   [:section {:class "mt-8"}
    [:div {:class "mb-small"}
     [h/h4-md "Your database credentials"]
     [:label {:class "text-xs text-gray-500"}
      "Check how we store this information "
      [:a {:class "text-blue-500"
           :href "https://hoop.dev/docs/concepts/connections"
           :target "_blank"}
       "here"]]]
    [:div {:class "grid gap-x-regular"}
     (config-inputs/config-inputs-labeled configs {})]]

   [submit/main form-type current-agent-name current-agent-id @agents]])

(defn command-line-view
  [{:keys [connection-name
           connection-subtype
           review-toggle-enabled?
           approval-groups-value
           data-masking-toggle-enabled?
           data-masking-groups-value
           api-key]}]
  [:div
   [:div {:class "mb-large"}
    [instructions/install-hoop]]

   [:div {:class "mb-large"}
    [instructions/setup-token @api-key]]

   [:div {:class "mb-large"}
    [instructions/run-hoop-connection {:connection-name @connection-name
                                       :connection-subtype @connection-subtype
                                       :review? @review-toggle-enabled?
                                       :review-groups (js-select-options->list @approval-groups-value)
                                       :data-masking? @data-masking-toggle-enabled?
                                       :data-masking-fields (js-select-options->list @data-masking-groups-value)}]]

   [:div {:class "flex justify-end items-center"}
    [:span {:class "text-gray-400 text-sm whitespace-pre block"}
     "If you have finished the setup, you can "]
    [:span {:class "text-blue-500 text-sm cursor-pointer"
            :on-click (fn []
                        (rf/dispatch [:connections->get-connections])
                        (reset! connection-overlay/overlay-open? true))}
     "check your connections."]]])

(defn main []
  (let [user (rf/subscribe [:users->current-user])
        agents (rf/subscribe [:agents])
        selected-tab (r/atom "Command line")]
    (fn [configs {:keys [connection-name
                         connection-type
                         connection-subtype
                         connection-command
                         tags-value
                         tags-input-value
                         user-groups
                         current-agent-name
                         current-agent-id
                         review-toggle-enabled?
                         approval-groups-value
                         data-masking-toggle-enabled?
                         data-masking-groups-value
                         access-schema-toggle-enabled?
                         access-mode-runbooks
                         access-mode-connect
                         access-mode-exec
                         api-key
                         form-type]}]
      [:<>
       [:section {:class "mb-large"}
        [:div {:class "mb-small"}
         [h/h4-md "Choose your Database type"]
         [:label {:class "text-xs text-gray-500"}
          "Check our supported database "
          [:a {:class "text-blue-500"
               :href (str "https://hoop.dev/docs/connections")
               :target "_blank"}
           "here"]]]
        [:> ui/RadioGroup {:value @connection-subtype
                           :disabled (= form-type :update)
                           :onChange (fn [type]
                                       (reset! connection-type :database)
                                       (reset! connection-subtype type)
                                       (reset! configs (utils/get-config-keys (keyword type)))
                                       (reset! connection-name (str type "-" (random-connection-name)))
                                       (reset! connection-command (get constants/connection-commands type)))}
         [:> (.-Label ui/RadioGroup) {:className "sr-only"}
          "Database connections"]
         [:div {:class "space-y-2"}
          (for [database [{:type "postgres" :label "PostgreSQL"}
                          {:type "mysql" :label "MySQL"}
                          {:type "mssql" :label "Microsoft SQL"}
                          {:type "mongodb" :label "MongoDB"}]]
            ^{:key (:type database)}
            [:> (.-Option ui/RadioGroup)
             {:value (:type database)
              :className (fn [params]
                           (str "relative flex cursor-pointer flex-col rounded-lg border p-4 focus:outline-none md:grid md:grid-cols-3 md:pl-4 md:pr-6 "
                                (if (.-checked params)
                                  "z-10 bg-gray-900"
                                  "border-gray-200")))}
             (fn [params]
               (r/as-element
                [:<>
                 [:span {:class "flex items-center text-sm"}
                  [:span {:aria-hidden "true"
                          :class (str "h-4 w-4 rounded-full border bg-white flex items-center justify-center "
                                      (if (.-checked params)
                                        "border-transparent"
                                        "border-gray-300")
                                      (when (.-active params)
                                        "ring-2 ring-offset-2 ring-indigo-600 "))}
                   [:span {:class (str "rounded-full w-1.5 h-1.5 "
                                       (if (.-checked params)
                                         "bg-gray-900"
                                         "bg-white"))}]]
                  [:> (.-Label ui/RadioGroup) {:as "span"
                                               :className (str "ml-3 font-medium "
                                                               (if (.-checked params)
                                                                 "text-white"
                                                                 "text-gray-700"))}
                   (:label database)]]]))])]]]

       [:section {:class "space-y-8 mb-16"}
        [toggle-review/main {:free-license? (:free-license? (:data @user))
                             :user-groups user-groups
                             :review-toggle-enabled? review-toggle-enabled?
                             :approval-groups-value approval-groups-value}]

        [toggle-data-masking/main {:free-license? (:free-license? (:data @user))
                                   :data-masking-toggle-enabled? data-masking-toggle-enabled?
                                   :data-masking-groups-value data-masking-groups-value}]

        [:div {:class "flex justify-between items-center"}
         [:div {:class "mr-24"}
          [:div {:class "flex items-center gap-2"}
           [h/h4-md "Enable schema in the Editor"]]
          [:label {:class "text-xs text-gray-500"}
           "Show database schemas in the connection details"]]
         [toggle/main {:enabled? @access-schema-toggle-enabled?
                       :on-click (fn []
                                   (reset! access-schema-toggle-enabled?
                                           (not @access-schema-toggle-enabled?)))}]]

        [:div {:class " flex flex-col gap-4"}
         [:div {:class "mr-24"}
          [:div {:class "flex items-center gap-2"}
           [h/h4-md "Enable custom access modes"]]
          [:label {:class "text-xs text-gray-500"}
           "Choose what users can run in this connection"]]

         [checkboxes/group
          [{:name "runbooks"
            :label "Runbooks"
            :description "Create templates to automate tasks in your organization"
            :checked? access-mode-runbooks}
           {:name "connect"
            :label "Native"
            :description "Access from your client of preference using hoop.dev to channel connections using our Desktop App or our Command Line Interface"
            :checked? access-mode-connect}
           {:name "exec"
            :label "Web & One-Offs"
            :description "Use hoop.dev's developer portal or our CLI's One-Offs commands directly in your terminal "
            :checked? access-mode-exec}]]]

        [multi-select/text-input {:value tags-value
                                  :input-value tags-input-value
                                  :disabled? false
                                  :required? false
                                  :on-change (fn [value]
                                               (reset! tags-value value))
                                  :on-input-change (fn [value]
                                                     (reset! tags-input-value value))
                                  :label "Tags"
                                  :label-description "Categorize your connections with specific identifiers"
                                  :id "tags-multi-select-text-input"
                                  :name "tags-multi-select-text-input"}]]

       (if (= form-type :create)
         (if (= @connection-subtype "mongodb")
           [:section {:class "mb-large"}
            [manual-credentials-view
             configs
             agents
             {:connection-type connection-type
              :current-agent-name current-agent-name
              :current-agent-id current-agent-id
              :form-type form-type}]]

           [:section {:class "mb-large"}
            [h/h4-md "Choose your setup method"
             {:class "font-bold mb-4"}]
            [tabs/tabs {:default-value @selected-tab
                        :on-change #(reset! selected-tab %)
                        :tabs ["Command line" "Manually with credentials"]}]
            (case @selected-tab
              "Command line" [command-line-view {:connection-name connection-name
                                                 :connection-subtype connection-subtype
                                                 :review-toggle-enabled? review-toggle-enabled?
                                                 :approval-groups-value approval-groups-value
                                                 :data-masking-toggle-enabled? data-masking-toggle-enabled?
                                                 :data-masking-groups-value data-masking-groups-value
                                                 :api-key api-key}]

              "Manually with credentials" [manual-credentials-view
                                           configs
                                           agents
                                           {:connection-type connection-type
                                            :current-agent-name current-agent-name
                                            :current-agent-id current-agent-id
                                            :form-type form-type}])])

         [:section {:class "mb-large"}
          [manual-credentials-view
           configs
           agents
           {:connection-type connection-type
            :current-agent-name current-agent-name
            :current-agent-id current-agent-id
            :form-type form-type}]])])))
