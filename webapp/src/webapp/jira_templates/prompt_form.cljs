(ns webapp.jira-templates.prompt-form
  (:require
   ["@radix-ui/themes" :refer [Button Flex Box Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.jira-templates.form-data :as form-data-fns]
   [webapp.components.paginated-searchbox :as paginated-searchbox]))

(defn- create-cmdb-select-options [jira-values]
  (mapv (fn [{:keys [id name type]}]
          {:value id
           :label name
           :description type})
        jira-values))

(defn- render-field [{:keys [label required jira_field field_type field_options form-data on-change]}]
  (case field_type
    "select" [forms/select {:label label
                            :required required
                            :full-width? true
                            :options (mapv #(hash-map :value % :text %) field_options)
                            :selected (get-in @form-data [:jira_fields jira_field] "")
                            :on-change #(swap! form-data assoc-in [:jira_fields jira_field] %)}]

    "datetime-local" [forms/input {:label label
                                   :required required
                                   :type "datetime-local"
                                   :value (get-in @form-data [:jira_fields jira_field] "")
                                   :on-change on-change}]

    ;; default text input
    [forms/input {:label label
                  :required required
                  :placeholder label
                  :value (get-in @form-data [:jira_fields jira_field] "")
                  :on-change on-change}]))

(defn- cmdb-field [cmdb-item all-items fieldconfigs template-id form-data]
  (let [field (:jira_field cmdb-item)
        pagination (rf/subscribe [:jira-templates->cmdb-pagination field])
        search-term (rf/subscribe [:jira-templates->cmdb-search field])
        loading? (rf/subscribe [:jira-templates->cmdb-loading? field])
        options (create-cmdb-select-options (:jira_values cmdb-item))]

    [:div.mb-4
     [:label.block.text-xs.font-semibold.text-gray-800.mb-1
      (:label cmdb-item)
      (when (:required cmdb-item)
        [:span.text-red-500 " *"])]
     [paginated-searchbox/main
      {:options options
       :loading? @loading?
       :selected-value (:value cmdb-item)
       :placeholder "Select an option"
       :total-items (:total-items @pagination)
       :current-page (:page @pagination)
       :items-per-page (:per-page @pagination)
       :required? (:required cmdb-item)
       :name (:jira_field cmdb-item)
       :on-search (fn [term]
                    (rf/dispatch [:jira-templates->set-cmdb-search cmdb-item term])
                    (rf/dispatch [:jira-templates->get-cmdb-values template-id cmdb-item 1 term]))
       :on-page-change (fn [page]
                         (rf/dispatch [:jira-templates->get-cmdb-values
                                       template-id cmdb-item page @search-term]))
       :on-select (fn [value]
                    (rf/dispatch [:jira-templates->update-cmdb-value cmdb-item value])
                    (swap! form-data form-data-fns/select-cmdb cmdb-item all-items fieldconfigs value))}]]))

(defn main [{:keys [prompts on-submit]}]
  (let [cmdb-items (rf/subscribe [:jira-templates->submit-template-cmdb-items])
        fieldconfigs (rf/subscribe [:jira-templates->cmdb-fieldconfigs])
        template-id (rf/subscribe [:jira-templates->submit-template-id])
        form-data (r/atom (form-data-fns/init-form-data @cmdb-items))]
    (fn []
      [:> Box {:class "p-6"}
       [:> Text {:as "h3" :size "5" :weight "bold" :mb "4"}
        "Required information"]
       [:> Text {:as "p" :size "2" :mb "5"}
        "Fill out following information to proceed with your command request."]

       [:form {:on-submit (fn [e]
                            (.preventDefault e)
                            (on-submit (form-data-fns/submit-data prompts @form-data)))}

        [:> Flex {:direction "column" :gap "4"}
         (when (seq prompts)
           (for [{:keys [label required jira_field field_type field_options]} prompts]
             ^{:key jira_field}
             [render-field
              {:label label
               :required required
               :jira_field jira_field
               :field_type field_type
               :field_options field_options
               :form-data form-data
               :on-change #(swap! form-data assoc-in [:jira_fields jira_field] (.. % -target -value))}]))

         (when (seq @cmdb-items)
           (doall
            (for [item @cmdb-items]
              ^{:key (:jira_field item)}
              [cmdb-field item @cmdb-items @fieldconfigs @template-id form-data])))]

        [:> Flex {:justify "end" :gap "3" :mt "6"}
         [:> Button {:variant "soft"
                     :color "gray"
                     :type "button"
                     :on-click #(rf/dispatch [:modal->close])}
          "Cancel"]
         [:> Button {:type "submit"}
          "Send"]]]])))
