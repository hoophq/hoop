(ns webapp.connections.views.form.ssh
  (:require [re-frame.core :as rf]
            [webapp.components.checkboxes :as checkboxes]
            [webapp.components.forms :as forms]
            [webapp.components.headings :as h]
            [webapp.components.multiselect :as multi-select]
            [webapp.connections.views.configuration-inputs :as config-inputs]
            [webapp.connections.views.form.submit :as submit]
            [webapp.connections.views.form.toggle-data-masking :as toggle-data-masking]
            [webapp.connections.views.form.toggle-review :as toggle-review]
            [webapp.formatters :as f]))

(defn manual-credentials-view
  [configs
   configs-file
   agents
   {:keys [current-agent-name
           current-agent-id
           config-file-value
           form-type]}]
  [:<>
   [:section {:class "mt-8"}
    [:div {:class "mb-small"}
     [h/h4-md "Your SSH credentials"]
     [:label {:class "text-xs text-gray-500"}
      "Check how we store this information "
      [:a {:class "text-blue-500"
           :href "https://hoop.dev/docs/concepts/connections"
           :target "_blank"}
       "here"]]]
    [:div {:class "grid gap-x-regular"}
     [:<>
      (config-inputs/config-inputs-labeled configs {})

      (if (empty? @configs-file)
        [:<>
         [forms/textarea {:label "SSH private key file content"
                          :required true
                          :placeholder "Paste your file content here"
                          :on-change #(reset! config-file-value (-> % .-target .-value))
                          :value @config-file-value}]]

        (config-inputs/config-inputs-files configs-file {}))]]]

   [submit/main form-type current-agent-name current-agent-id @agents]])

(defn nickname-input [connection-name connection-type form-type]
  [:<>
   [:label {:class "text-xs text-gray-500 my-small"}
    "This name identifies your connection and should be unique"]
   [forms/input {:label "Name"
                 :placeholder (str "my-" @connection-type "-test")
                 :disabled (= form-type :update)
                 :on-change (fn [v]
                              (reset! connection-name (f/replace-empty-space->dash (-> v .-target .-value))))
                 :required true
                 :value @connection-name}]])

(defn main []
  (let [user (rf/subscribe [:users->current-user])
        agents (rf/subscribe [:agents])]
    (fn [configs configs-file {:keys [connection-name
                                      connection-type
                                      current-agent-name
                                      current-agent-id
                                      tags-value
                                      tags-input-value
                                      user-groups
                                      review-toggle-enabled?
                                      approval-groups-value
                                      data-masking-toggle-enabled?
                                      data-masking-groups-value
                                      access-mode-runbooks
                                      access-mode-connect
                                      access-mode-exec
                                      config-file-value
                                      form-type]}]
      [:<>
       [:section {:class "mb-large"}
        [:div {:class "grid grid-cols-1"}
         [h/h4-md
          (str "Setup your SSH")]
         [nickname-input connection-name connection-type form-type]]]
       [:section {:class "space-y-8 mb-16"}
        [toggle-review/main {:free-license? (:free-license? (:data @user))
                             :user-groups user-groups
                             :review-toggle-enabled? review-toggle-enabled?
                             :approval-groups-value approval-groups-value}]

        [toggle-data-masking/main {:free-license? (:free-license? (:data @user))
                                   :data-masking-toggle-enabled? data-masking-toggle-enabled?
                                   :data-masking-groups-value data-masking-groups-value}]

        [:div {:class " flex flex-col gap-4"}
         [:div {:class "mr-24"}
          [:div {:class "flex items-center gap-2"}
           [h/h4-md "Enable custom access modes"]]
          [:label {:class "text-xs text-gray-500"}
           "Choose what users can run in this connection"]]

         [checkboxes/group
          [{:name "runbooks"
            :label "Runbooks"
            :description [:<>
                          [:p "Create templates to automate tasks in your organization"]
                          [:p "*Not available for SSH connections"]]
            :disabled? true
            :checked? access-mode-runbooks}
           {:name "connect"
            :label "Native"
            :description "Access from your client of preference using hoop.dev to channel connections using our Desktop App or our Command Line Interface"
            :checked? access-mode-connect}
           {:name "exec"
            :label "Web & One-Offs"
            :disabled? true
            :description [:<>
                          [:p "Use hoop.dev's developer portal or our CLI's One-Offs commands directly in your terminal"]
                          [:p "*Not available for SSH connections"]]
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
         [:section {:class "mb-large"}
          [manual-credentials-view
           configs
           configs-file
           agents
           {:current-agent-name current-agent-name
            :current-agent-id  current-agent-id
            :config-file-value config-file-value
            :form-type form-type}]]

         [:section {:class "mb-large"}
          [manual-credentials-view
           configs
           configs-file
           agents
           {:current-agent-name current-agent-name
            :current-agent-id  current-agent-id
            :config-file-value config-file-value
            :form-type form-type}]])])))
