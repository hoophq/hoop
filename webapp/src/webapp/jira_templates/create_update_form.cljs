(ns webapp.jira-templates.create-update-form
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.jira-templates.basic-info :as basic-info]
   [webapp.jira-templates.cmdb-table :as cmdb-table]
   [webapp.jira-templates.connections-section :as connections-section]
   [webapp.jira-templates.form-header :as form-header]
   [webapp.jira-templates.helpers :as helpers]
   [webapp.jira-templates.mapping-table :as mapping-table]
   [webapp.jira-templates.preset-mapping-table :as preset-mapping-table]
   [webapp.jira-templates.prompts-table :as prompts-table]
   [webapp.jira-templates.workflow-info :as workflow-info]))

(defn jira-form [form-type template scroll-pos]
  (let [state (helpers/create-form-state template)
        handlers (helpers/create-form-handlers state)
        submitting? (rf/subscribe [:jira-templates->submitting?])]
    (fn []
      [:> Box {:class "min-h-screen bg-gray-1"}
       [:form {:id "jira-form"
               :on-submit (fn [e]
                            (.preventDefault e)
                            (let [data (helpers/prepare-payload state)]
                              (if (= :edit form-type)
                                (rf/dispatch [:jira-templates->update-by-id data])
                                (rf/dispatch [:jira-templates->create data]))))}

        [form-header/main
         {:form-type form-type
          :id @(:id state)
          :scroll-pos scroll-pos
          :loading? @submitting?}]

        [:> Box {:p "7" :class "space-y-radix-9"}
         [basic-info/main
          {:name (:name state)
           :description (:description state)
           :project-key (:project_key state)
           :request-type-id (:request_type_id state)
           :on-name-change #(reset! (:name state) %)
           :on-description-change #(reset! (:description state) %)
           :on-project-key-change #(reset! (:project_key state) %)
           :on-request-type-id-change #(reset! (:request_type_id state) %)}]

         [workflow-info/main
          {:status (:issue_transition_name_on_close state)
           :on-status-change #(reset! (:issue_transition_name_on_close state) %)}]

         ;; Connections section
         [connections-section/main
          {:connection-ids (:connection_ids state)
           :on-connections-change (:on-connections-change handlers)}]

         [:> Flex {:direction "column" :gap "5"}
          [:> Box
           [:> Flex {:align "center" :gap "2"}
            [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
             "Configure resource role tags mapping"]]
           [:> Text {:size "3" :class "text-[--gray-11]"}
            "Match key-value information in Jira fields with your resource role tags."]]

          [:> Box {:class "space-y-radix-7"}
           [preset-mapping-table/main
            (merge
             {:state (:mapping state)
              :select-state (:mapping-select-state state)}
             (select-keys handlers
                          [:on-mapping-field-change
                           :on-mapping-select
                           :on-toggle-mapping-select
                           :on-toggle-all-mapping
                           :on-mapping-delete
                           :on-mapping-add]))]]]

         [:> Flex {:direction "column" :gap "5"}
          [:> Box
           [:> Flex {:align "center" :gap "2"}
            [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
             "Configure automated mapping"]]
           [:> Text {:size "3" :class "text-[--gray-11]"}
            "Append additional information to your Jira cards when executing a command in your resource roles."]]

          [:> Box {:class "space-y-radix-7"}
           [mapping-table/main
            (merge
             {:state (:mapping state)
              :select-state (:mapping-select-state state)}
             (select-keys handlers
                          [:on-mapping-field-change
                           :on-mapping-select
                           :on-toggle-mapping-select
                           :on-toggle-all-mapping
                           :on-mapping-delete
                           :on-mapping-add]))]]]

         [:> Flex {:direction "column" :gap "5"}
          [:> Box
           [:> Flex {:align "center" :gap "2"}
            [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
             "Configure manual prompt"]]
           [:> Text {:size "3" :class "text-[--gray-11]"}
            "Request additional information from executed commands."]]

          [:> Box {:class "space-y-radix-7"}
           [prompts-table/main
            (merge
             {:state (:prompts state)
              :select-state (:prompts-select-state state)}
             (select-keys handlers
                          [:on-prompt-field-change
                           :on-prompt-select
                           :on-toggle-prompt-select
                           :on-toggle-all-prompts
                           :on-prompt-delete
                           :on-prompt-add]))]]]

         [:> Flex {:direction "column" :gap "5"}
          [:> Box
           [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
            "Set a configuration management database (CMDB)"]
           [:> Text {:size "3" :class "text-[--gray-11]"}
            "Create an additional layer of relation between CMDBs and hoop services."]]

          [:> Box {:class "space-y-radix-7"}
           [cmdb-table/main
            (merge
             {:state (:cmdb state)
              :select-state (:cmdb-select-state state)}
             (select-keys handlers
                          [:on-cmdb-field-change
                           :on-cmdb-select
                           :on-toggle-cmdb-select
                           :on-toggle-all-cmdb
                           :on-cmdb-delete
                           :on-cmdb-add]))]]]]]])))

(defn- loading []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn main [form-type]
  (let [jira-template (rf/subscribe [:jira-templates->active-template])
        scroll-pos (r/atom 0)]
    (fn []
      (r/with-let [handle-scroll #(reset! scroll-pos (.-scrollY js/window))]
        (.addEventListener js/window "scroll" handle-scroll)

        (if (= :loading (:status @jira-template))
          [loading]
          [jira-form form-type (:data @jira-template) scroll-pos])

        (finally
          (.removeEventListener js/window "scroll" handle-scroll)
          (rf/dispatch [:jira-templates->clear-active-template]))))))
