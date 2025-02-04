(ns webapp.connections.views.setup.configuration-inputs
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text]]
   ["lucide-react" :refer [Plus]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]))

(defn environment-variables-section []
  (let [current-key @(rf/subscribe [:connection-setup/env-current-key])
        current-value @(rf/subscribe [:connection-setup/env-current-value])
        env-vars @(rf/subscribe [:connection-setup/environment-variables])]
    [:> Box {:class "space-y-4"}
     [:> Heading {:size "3"} "Environment variables"]
     [:> Text {:size "2" :color "gray"}
      "Add variable values to use in your connection."]

     (when (seq env-vars)
       [:> Grid {:columns "2" :gap "2"}
        (for [[idx {:keys [key value]}] (map-indexed vector env-vars)]
          ^{:key (str "env-var-" idx)}
          [:<>
           [forms/input
            {:label "Key"
             :value key
             :placeholder "API_KEY"
             :on-change #(rf/dispatch [:connection-setup/update-env-var idx :key (-> % .-target .-value)])}]
           [forms/input
            {:label "Value"
             :value value
             :type "password"
             :placeholder "* * * *"
             :on-change #(rf/dispatch [:connection-setup/update-env-var idx :value (-> % .-target .-value)])}]])])

     ;; Inputs atuais
     [:> Grid {:columns "2" :gap "2"}
      [forms/input
       {:label "Key"
        :placeholder "API_KEY"
        :value current-key
        :on-change #(rf/dispatch [:connection-setup/update-env-current-key (-> % .-target .-value)])}]
      [forms/input
       {:label "Value"
        :placeholder "* * * *"
        :type "password"
        :value current-value
        :on-change #(rf/dispatch [:connection-setup/update-env-current-value (-> % .-target .-value)])}]]

     ;; Botão de adicionar
     [:> Button
      {:size "2"
       :variant "soft"
       :type "button"
       :on-click #(rf/dispatch [:connection-setup/add-env-row])}
      [:> Plus {:size 16}]
      "Add"]]))

(defn configuration-files-section []
  (let [config-files (rf/subscribe [:connection-setup/configuration-files])
        current-name (rf/subscribe [:connection-setup/config-current-name])
        current-content (rf/subscribe [:connection-setup/config-current-content])]
    [:> Box {:class "space-y-4"}
     [:> Heading {:size "3"} "Configuration files"]
     [:> Text {:size "2" :color "gray"}
      "Add values from your configuration file and use them as an environment variable in your connection."]

     ;; Lista de arquivos existentes com inputs editáveis
     (when (seq @config-files)
       [:> Grid {:columns "1" :gap "4"}
        (for [[idx {:keys [key value]}] (map-indexed vector @config-files)]
          ^{:key (str "config-file-" idx)}
          [:<>
           [forms/input
            {:label "Name"
             :value key
             :placeholder "e.g. kubeconfig"
             :on-change #(rf/dispatch [:connection-setup/update-config-file idx :key (-> % .-target .-value)])}]
           [forms/textarea
            {:label "Content"
             :id (str "config-file-" idx)
             :value value
             :on-change #(rf/dispatch [:connection-setup/update-config-file idx :value (-> % .-target .-value)])}]])])

     ;; Campos para novo arquivo
     [:> Grid {:columns "1" :gap "4"}
      [forms/input
       {:label "Name"
        :placeholder "e.g. kubeconfig"
        :value @current-name
        :on-change #(rf/dispatch [:connection-setup/update-config-file-name
                                  (-> % .-target .-value)])}]
      [forms/textarea
       {:label "Content"
        :id "config-file-initial"
        :placeholder "Paste your file content here"
        :value @current-content
        :on-change #(rf/dispatch [:connection-setup/update-config-file-content
                                  (-> % .-target .-value)])}]]

     [:> Button
      {:size "2"
       :variant "soft"
       :type "button"
       :on-click #(when (and (not (empty? @current-name))
                             (not (empty? @current-content)))
                    (rf/dispatch [:connection-setup/add-configuration-file]))}
      [:> Plus {:size 16
                :class "mr-2"}]
      "Add file"]]))
