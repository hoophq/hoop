(ns webapp.connections.views.form.custom
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.forms :as forms]
            [webapp.components.headings :as h]
            [webapp.components.multiselect :as multi-select]
            [webapp.components.tabs :as tabs]
            [webapp.connections.views.configuration-inputs :as config-inputs]
            [webapp.connections.views.form.hoop-run-instructions :as instructions]
            [webapp.connections.views.form.submit :as submit]
            [webapp.connections.views.form.toggle-data-masking :as toggle-data-masking]
            [webapp.connections.views.form.toggle-review :as toggle-review]
            [webapp.shared-ui.sidebar.connection-overlay :as connection-overlay]))

(defn js-select-options->list [options]
  (mapv #(get % "value") options))

(defn manual-credentials-view
  [configs
   configs-file
   agents
   {:keys [current-agent-name
           current-agent-id
           connection-command
           config-file-name
           config-file-value
           config-key
           config-value
           on-click->add-more
           on-click->add-more-file
           form-type]}]
  [:<>
   [:section {:class "my-large"}
    [:div {:class "mb-small"}
     [h/h4-md "Load environment variables"]
     [:label {:class "text-xs text-gray-500"}
      "Add environment variable values and use them in your command"]]

    [:div {:class "grid grid-cols-2 gap-small"}
     (config-inputs/config-inputs configs {})

     [:<>
      [forms/input {:label "Key"
                    :on-change #(reset! config-key (-> % .-target .-value))
                    :classes "whitespace-pre overflow-x"
                    :placeholder "API_KEY"
                    :value @config-key}]
      [forms/input {:label "Value"
                    :on-change #(reset! config-value (-> % .-target .-value))
                    :classes "whitespace-pre overflow-x"
                    :placeholder "* * * *"
                    :type "password"
                    :value @config-value}]]]


    [:div {:class "grid grid-cols-1 justify-items-end mb-4"}
     (button/secondary {:text "+ add more"
                        :on-click #(on-click->add-more)
                        :variant :small})]]

   [:section {:class "mb-8"}
    [:div {:class "mb-small"}
     [h/h4-md "Load configuration files"]
     [:label {:class "text-xs text-gray-500 mt-small"}
      "Add values from your configuration file and use them as an environment variable in your command"]]
    [:div {:class "grid gap-x-regular"}
     (config-inputs/config-inputs-files configs-file {})

     [forms/input {:label "File name"
                   :classes "whitespace-pre overflow-x"
                   :placeholder "kubeconfig"
                   :on-change #(reset! config-file-name (-> % .-target .-value))
                   :value @config-file-name}]
     [forms/textarea {:label "File content"
                      :placeholder "Paste your file content here"
                      :on-change #(reset! config-file-value (-> % .-target .-value))
                      :value @config-file-value}]]

    [:div {:class "grid grid-cols-1 justify-items-end mb-4"}
     (button/secondary {:text "+ add more"
                        :on-click #(on-click->add-more-file)
                        :variant :small})]]

   [:section {:class "mb-8"}
    [:div {:class "mb-small"}
     [h/h4-md "Command"]
     [:label {:class "block text-xs text-gray-500 mt-small"}
      "The command that will run on your connection."]
     [:label {:class "text-xs text-gray-500"}
      "The environment variables loaded above can be used here."]]
    [forms/textarea {:on-change #(reset! connection-command (-> % .-target .-value))
                     :placeholder "$ your command"
                     :id "command-line"
                     :rows 2
                     :value @connection-command}]]

   [submit/main form-type current-agent-name current-agent-id @agents]])

(defn command-line-view [{:keys [connection-name
                                 connection-subtype
                                 api-key
                                 review-toggle-enabled?
                                 approval-groups-value
                                 data-masking-toggle-enabled?
                                 data-masking-groups-value]}]
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
    (fn [configs configs-file {:keys [connection-name
                                      connection-subtype
                                      current-agent-name
                                      current-agent-id
                                      tags-value
                                      tags-input-value
                                      user-groups
                                      api-key
                                      review-toggle-enabled?
                                      approval-groups-value
                                      data-masking-toggle-enabled?
                                      data-masking-groups-value
                                      connection-command
                                      config-file-name
                                      config-file-value
                                      config-key
                                      config-value
                                      on-click->add-more
                                      on-click->add-more-file
                                      form-type]}]
      [:<>
       [:section {:class "mb-large"}
        [toggle-review/main {:free-license? (:free-license? (:data @user))
                             :user-groups user-groups
                             :review-toggle-enabled? review-toggle-enabled?
                             :approval-groups-value approval-groups-value}]

        [toggle-data-masking/main {:free-license? (:free-license? (:data @user))
                                   :data-masking-toggle-enabled? data-masking-toggle-enabled?
                                   :data-masking-groups-value data-masking-groups-value}]
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
         [:section {:class "mb-large"}
          [tabs/tabs {:on-change #(reset! selected-tab %)
                      :tabs ["Command line" "Manually with credentials"]}]
          (case @selected-tab
            "Command line" [command-line-view {:connection-name connection-name
                                               :connection-subtype connection-subtype
                                               :api-key api-key
                                               :review-toggle-enabled? review-toggle-enabled?
                                               :approval-groups-value approval-groups-value
                                               :data-masking-toggle-enabled? data-masking-toggle-enabled?
                                               :data-masking-groups-value data-masking-groups-value}]
            "Manually with credentials" [manual-credentials-view
                                         configs
                                         configs-file
                                         agents
                                         {:current-agent-name current-agent-name
                                          :current-agent-id  current-agent-id
                                          :connection-command connection-command
                                          :config-file-name config-file-name
                                          :config-file-value config-file-value
                                          :config-key config-key
                                          :config-value config-value
                                          :on-click->add-more on-click->add-more
                                          :on-click->add-more-file on-click->add-more-file
                                          :form-type form-type}])]

         [:section {:class "mb-large"}
          [manual-credentials-view
           configs
           configs-file
           agents
           {:current-agent-name current-agent-name
            :current-agent-id  current-agent-id
            :connection-command connection-command
            :config-file-name config-file-name
            :config-file-value config-file-value
            :config-key config-key
            :config-value config-value
            :on-click->add-more on-click->add-more
            :on-click->add-more-file on-click->add-more-file
            :form-type form-type}]])])))
