(ns webapp.connections.views.create-update-connection.connection-environment-form
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Grid Link Text]]
   ["lucide-react" :refer [ArrowUpRight Plus]]
   [webapp.components.forms :as forms]
   [webapp.connections.helpers :as helpers]
   [webapp.connections.views.configuration-inputs :as config-inputs]
   [webapp.connections.views.create-update-connection.hoop-run-instructions :as instructions]))

(defn main []
  (fn [{:keys [agents
               agent-id
               api-key
               connection-name
               connection-type
               connection-subtype
               configs
               config-key
               config-value
               configs-file
               config-file-name
               config-file-value
               connection-command
               reviews
               review-groups
               ai-data-masking
               ai-data-masking-info-types
               on-click->add-more-key-value
               on-click->add-more-file-content]}]
    (let [agent-options (mapv (fn [agent] {:text (:name agent) :value (:id agent)}) (:data agents))]
      [:> Flex {:direction "column" :gap "9" :class "px-20"}

       (when-not  (and (= @connection-type "application")
                       (not= @connection-subtype "tcp"))
         [:> Grid {:columns "5" :gap "7"}
          [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
           [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Set an agent"]
           [:> Text {:size "3" :class "text-gray-11"} "Select an agent to provide a secure interaction with your connection."]
           [:> Link {:href "https://hoop.dev/docs/concepts/agent"
                     :target "_blank"}
            [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
             [:> Callout.Icon
              [:> ArrowUpRight {:size 16}]]
             [:> Callout.Text
              "Learn more about Agents"]]]]

          [:> Flex {:direction "column" :gap "7" :grid-column "span 3 / span 3"}
           [:> Box {:class "space-y-radix-5"}
            [forms/select {:label "Agent"
                           :placeholder "Select one"
                           :full-width? true
                           :class "w-full"
                           :selected (or @agent-id "")
                           :required true
                           :on-change #(reset! agent-id %)
                           :options agent-options}]]]])

       (cond
         (= @connection-type "database")
         [:> Grid {:columns "5" :gap "7"}
          [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
           [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Database credentials"]
           [:> Text {:size "3" :class "text-gray-11"} "Provide your database access information."]]
          [:> Grid {:gap-x "7" :grid-column "span 3 / span 3"}
           (config-inputs/config-inputs-labeled configs {})]]

         (and (= @connection-type "custom")
              (= @connection-subtype "ssh"))
         [:> Grid {:columns "5" :gap "7"}
          [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
           [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "SSH Credentials"]
           [:> Text {:size "3" :class "text-gray-11"} "Provide SSH information to setup your connection."]]
          [:> Grid {:gap-x "6" :grid-column "span 3 / span 3"}
           (if (empty? @configs-file)
             [:<>
              [forms/textarea {:label "Private Key"
                               :required true
                               :placeholder "Paste your file content here"
                               :on-change #(reset! config-file-value (-> % .-target .-value))
                               :value @config-file-value}]]

             (config-inputs/config-inputs-files configs-file {}))

           (config-inputs/config-inputs-labeled configs {})]]

         (and (= @connection-type "custom")
              (not= @connection-subtype "ssh"))
         [:<>
          [:> Grid {:columns "5" :gap "7"}
           [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
            [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Load environment variables"]
            [:> Text {:size "3" :class "text-gray-11"} "Add variables values to use in your connection."]]
           [:> Grid {:columns "2" :gap "2" :grid-column "span 3 / span 3"}
            (config-inputs/config-inputs configs {})
            [:<>
             [forms/input {:label "Key"
                           :full-width? true
                           :on-change #(reset! config-key (-> % .-target .-value))
                           :classes "whitespace-pre overflow-x"
                           :placeholder "API_KEY"
                           :value @config-key}]
             [forms/input {:label "Value"
                           :full-width? true
                           :on-change #(reset! config-value (-> % .-target .-value))
                           :classes "whitespace-pre overflow-x"
                           :placeholder "* * * *"
                           :type "password"
                           :value @config-value}]]
            [:> Box {:grid-column "span 2 / span 2" :class "justify-self-center"}
             [:> Button {:size "2"
                         :variant "ghost"
                         :type "button"
                         :color "gray"
                         :on-click #(on-click->add-more-key-value)}
              [:> Plus {:size 16
                        :class "mr-2"}]
              "Add more"]]]]
          [:> Grid {:columns "5" :gap "7"}
           [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
            [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Load configuration files"]
            [:> Text {:size "3" :class "text-gray-11"} "Add values from your configuration file and use them as an environment variable in your connection."]]
           [:> Grid {:gap-x "7" :grid-column "span 3 / span 3"}
            (config-inputs/config-inputs-files configs-file {})

            [forms/input {:label "Name"
                          :classes "whitespace-pre overflow-x"
                          :placeholder "kubeconfig"
                          :on-change #(reset! config-file-name (-> % .-target .-value))
                          :value @config-file-name}]
            [forms/textarea {:label "Content"
                             :placeholder "Paste your file content here"
                             :on-change #(reset! config-file-value (-> % .-target .-value))
                             :value @config-file-value}]
            [:> Box {:class "justify-self-center"}
             [:> Button {:size "2"
                         :variant "ghost"
                         :type "button"
                         :color "gray"
                         :on-click #(on-click->add-more-file-content)}
              [:> Plus {:size 16
                        :class "mr-2"}]
              "Add more"]]]]
          [:> Grid {:columns "5" :gap "7"}
           [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
            [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Additional command"]
            [:> Text {:size "3" :class "text-gray-11"} (str "Add an additional command that will run on your connection.\n\n"
                                                            "Environment variables loaded above can also be used here.")]]
           [:> Flex {:direction "column" :class "space-y-radix-7" :grid-column "span 3 / span 3"}
            [forms/textarea {:label "Command"
                             :on-change #(reset! connection-command (-> % .-target .-value))
                             :placeholder "$ your command"
                             :value @connection-command
                             :id "command-line"
                             :rows 2}]]]]

         (and (= @connection-type "application")
              (= @connection-subtype "tcp"))
         [:> Grid {:columns "5" :gap "7"}
          [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
           [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "TCP credentials"]
           [:> Text {:size "3" :class "text-gray-11"} "Provide your TCP access information."]]
          [:> Grid {:gap-x "7" :grid-column "span 3 / span 3"}
           (config-inputs/config-inputs-labeled configs {})]]

         (and (= @connection-type "application")
              (not= @connection-subtype "tcp"))
         [:<>
          [:section {:class "space-y-radix-9"}
           [instructions/install-hoop]


           [instructions/setup-token @api-key]


           [instructions/run-hoop-connection {:connection-name @connection-name
                                              :connection-subtype @connection-subtype
                                              :review? @reviews
                                              :review-groups (helpers/js-select-options->list @review-groups)
                                              :data-masking? @ai-data-masking
                                              :data-masking-fields (helpers/js-select-options->list @ai-data-masking-info-types)}]]])])))
