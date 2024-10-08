(ns webapp.connections.views.create-update-connection.connection-environment-form
  (:require ["@radix-ui/themes" :refer [Box Button Callout Flex Grid Link Text]]
            ["lucide-react" :refer [ArrowUpRight Plus]]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.forms :as forms]
            [webapp.connections.views.configuration-inputs :as config-inputs]
            [webapp.connections.views.form.hoop-run-instructions :as instructions]))

(defn js-select-options->list [options]
  (mapv #(get % "value") options))

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
    (let [agent-options (map (fn [agent] {:text (:name agent) :value (:id agent)}) (:data agents))]
      [:> Flex {:direction "column" :gap "9" :class "px-20"}

       (when-not  (and (= @connection-type "application")
                       (not= @connection-subtype "tcp"))
         [:> Grid {:columns "5" :gap "7"}
          [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
           [:> Text {:size "4" :weight "bold"} "Set an agent"]
           [:> Text {:size "3"} "Select an agent to provide a secure interaction with your connection."]
           [:> Link {:href "https://hoop.dev/docs/concepts/agent"}
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
                           :selected @agent-id
                           :required true
                           :on-change #(reset! agent-id %)
                           :options agent-options}]]]])

       (cond
         (= @connection-type "database")
         [:> Grid {:columns "5" :gap "7"}
          [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
           [:> Text {:size "4" :weight "bold"} "Database credentials"]
           [:> Text {:size "3"} "Provide your database access information."]]
          [:> Grid {:gap-x "7" :grid-column "span 3 / span 3"}
           (config-inputs/config-inputs-labeled configs {})]]

         (and (= @connection-type "custom")
              (= @connection-subtype "ssh"))
         [:> Grid {:columns "5" :gap "7"}
          [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
           [:> Text {:size "4" :weight "bold"} "Custom credentials"]
           [:> Text {:size "3"} "Provide your custom access information."]]
          [:> Grid {:gap-x "6" :grid-column "span 3 / span 3"}
           (if (empty? @configs-file)
             [:<>
              [forms/textarea {:label "private key"
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
            [:> Text {:size "4" :weight "bold"} "Load environment variables"]
            [:> Text {:size "3"} "Add variables values to use in your connection."]]
           [:> Grid {:columns "2" :gap "2" :grid-column "span 3 / span 3"}
            (println @configs)
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
            [:> Text {:size "4" :weight "bold"} "Load configuration files"]
            [:> Text {:size "3"} "Add values from your configuration file and use them as an environment variable in your connection."]]
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
            [:> Text {:size "4" :weight "bold"} "Additional command"]
            [:> Text {:size "3"} (str "Add an additional command that will run on your connection.\n\n"
                                      "Environment variables loaded above can also be used here.")]]
           [:> Flex {:direction "column" :class "space-y-radix-7" :grid-column "span 3 / span 3"}
            [forms/textarea {:label "Command"
                             :on-change #(reset! connection-command (-> % .-target .-value))
                             :placeholder "$ your command"
                             :id "command-line"
                             :rows 2
                             :value @connection-command}]]]]

         (and (= @connection-type "application")
              (= @connection-subtype "tcp"))
         [:> Grid {:columns "5" :gap "7"}
          [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
           [:> Text {:size "4" :weight "bold"} "TCP credentials"]
           [:> Text {:size "3"} "Provide your TCP access information."]]
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
                                              :review-groups (js-select-options->list @review-groups)
                                              :data-masking? @ai-data-masking
                                              :data-masking-fields (js-select-options->list @ai-data-masking-info-types)}]]

          [:div {:class "flex justify-end items-center"}
           [:> Text {:size "1" :weight "light"}
            "If you have finished the setup, you can "
            [:> Link {:size "1"
                      :href "#"
                      :on-click (fn []
                                  (rf/dispatch [:connections->get-connections])
                                  (rf/dispatch [:navigate :connections]))}
             "check your connections."]]]])])))
