(ns webapp.webclient.runbooks.form
  (:require ["@heroicons/react/16/solid" :as hero-micro-icon]
            ["@heroicons/react/24/solid" :as hero-solid-icon]
            ["@radix-ui/themes" :refer [Box Button]]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.forms :as forms]
            [webapp.webclient.runbooks.exec-multiples-runbook-list :as exec-multiples-runbooks-list]
            [webapp.config :as config]))

(defn dynamic-form
  [type {:keys [label
                on-change
                placeholder
                value
                pattern
                required
                minlength
                maxlength
                min
                max
                step
                helper-text
                options
                default-value]}]
  [:div
   (case type
     "select" [forms/select (merge
                             {:label label
                              :full-width? true
                              :required required
                              :on-change on-change
                              :selected (or value default-value "")
                              :options (map #(into {} {:value % :text %}) options)
                              :helper-text helper-text}
                             (when (and
                                    (not= required "false")
                                    (or required (nil? required)))
                               {:required true}))]
     "textarea" [forms/textarea (merge
                                 {:label label
                                  :placeholder (or placeholder (str "Define a value for " label))
                                  :value (or value default-value "")
                                  :on-change on-change
                                  :minLength minlength
                                  :maxLength maxlength
                                  :helper-text helper-text}
                                 (when (and
                                        (not= required "false")
                                        (or required (nil? required)))
                                   {:required true}))]
     [forms/input (merge
                   {:label label
                    :placeholder (or placeholder (str "Define a value for " label))
                    :value (or value default-value "")
                    :type type
                    :pattern pattern
                    :on-change on-change
                    :minLength minlength
                    :maxLength maxlength
                    :min min
                    :max max
                    :step step
                    :helper-text helper-text}
                   (when (and
                          (not= required "false")
                          (or required (nil? required)))
                     {:required true}))])])

(defn- error-view [error]
  [:div {:class "pt-large flex flex-col gap-regular items-center"}
   [:div {:class "flex flex-col items-center text-center"}
    [:div {:class " text-base font-bold"}
     "Error found."]
    [:div {:class " text-sm mb-large"}
     error]]])

(defmulti template-view identity)

(defmethod template-view :ready [_ _ _]
  (let [state (r/atom {})
        previous-template-name (r/atom nil)
        update-state #(swap! state assoc %1 %2)]
    (rf/dispatch [:connections->get-connections])
    (fn [_ template selected-connections connection-name]
      ;; TODO: This implementation was made to fix the behavior of defmethod not accepting the re-rendering
      ;; based on its own key.
      (if (nil? (:data template))
        [:div {:class "flex items-center justify-center h-full"}
         [:span {:class "text-gray-400 text-xl"}
          "No Runbook selected"]]

        (do
          ;; Reset state when template changes
          (when (and (-> template :data :name)
                     (not= (-> template :data :name) @previous-template-name))
            (reset! state {})
            (reset! previous-template-name (-> template :data :name)))

          ;; Initialize all params with empty strings or default values
          (when (-> template :data :params)
            (doseq [param (-> template :data :params)
                    :let [metadata ((keyword param) (-> template :data :metadata))]]
              (when (nil? (get @state param))
                (swap! state assoc param (or (:default metadata) "")))))

          [:div {:class "overflow-auto lg:overflow-hidden text-[--gray-12]"}
           [:section
            [:form
             {:on-submit (fn [e]
                           (.preventDefault e)
                           (if (> (count selected-connections) 1)
                             (let [has-jira-template? (some #(not (empty? (:jira_issue_template_id %)))
                                                            selected-connections)
                                   jira-integration-enabled? (= (-> @(rf/subscribe [:jira-integration->details])
                                                                    :data
                                                                    :status)
                                                                "enabled")]
                               (if (and has-jira-template? jira-integration-enabled?)
                                 (rf/dispatch [:dialog->open
                                               {:title "Running in multiple connections not allowed"
                                                :action-button? false
                                                :text "For now, it's not possible to run commands in multiple connections with Jira Templates activated. Please select just one connection before running your command."}])
                                 (reset! exec-multiples-runbooks-list/atom-exec-runbooks-list-open? true)))

                             (let [connection (first (filter #(= (:name %) connection-name)
                                                             selected-connections))
                                   has-jira-template? (and connection
                                                           (not (empty? (:jira_issue_template_id connection))))
                                   jira-integration-enabled? (= (-> @(rf/subscribe [:jira-integration->details])
                                                                    :data
                                                                    :status)
                                                                "enabled")]
                               (if (and has-jira-template? jira-integration-enabled?)
                                 (rf/dispatch [:runbooks-plugin/show-jira-form
                                               {:template-id (:jira_issue_template_id connection)
                                                :file-name (-> template :data :name)
                                                :params @state
                                                :connection-name connection-name}])
                                 (rf/dispatch [:editor-plugin->run-runbook
                                               {:file-name (-> template :data :name)
                                                :params @state
                                                :connection-name connection-name}])))))}
             [:header {:class "mb-regular"}
              [:> Box {:class "flex items-center gap-small mb-small"}
               [:> hero-solid-icon/DocumentIcon
                {:class "h-4 w-4" :aria-hidden "true"}]
               [:span {:class "text-base font-semibold break-words"}
                (-> template :data :name)]]

              [:span {:class " text-xs"}
               "Fill the params below for this Runbook"]]
             (doall (for [param (-> template :data :params)
                          :let [metadata ((keyword param) (-> template :data :metadata))]]
                      ^{:key param}
                      [dynamic-form
                       (:type metadata) {:label param
                                         :placeholder (:placeholder metadata)
                                         :value (get @state param "")
                                         :type (:type metadata)
                                         :required (:required metadata)
                                         :on-change (if (= "select" (:type metadata))
                                                      #(update-state param %)
                                                      #(update-state param (-> % .-target .-value)))
                                         :helper-text (:description metadata)
                                         :options (:options metadata)
                                         :default-value (:default metadata)}]))

             (if (nil? (-> template :data :error))
               [:footer {:class "flex gap-regular justify-end"}
                [:> Button {:disabled (or (= (-> template :status) :loading)
                                          (= (-> template :form-status) :loading)
                                          (empty? selected-connections))
                            :class (when (or (= (-> template :status) :loading)
                                             (= (-> template :form-status) :loading)
                                             (empty? selected-connections))
                                     "cursor-not-allowed")
                            :type "submit"}
                 "Execute runbook"]]

               [error-view (-> template :data :error)])]]

           (when @exec-multiples-runbooks-list/atom-exec-runbooks-list-open?
             [exec-multiples-runbooks-list/main (map #(into {} {:connection-name (:name %)
                                                                :file_name (-> template :data :name)
                                                                :parameters @state
                                                                :type (:type %)
                                                                :subtype (:subtype %)
                                                                :session-id nil
                                                                :status :ready})
                                                     selected-connections)])])))))

(defmethod template-view :loading []
  [:div {:class "flex items-center justify-center h-full"}
   [:figure {:class "w-8"}
    [:img {:class "animate-spin"
           :src (str config/webapp-url "/icons/icon-loader-circle.svg")}]]])

(defmethod template-view :default []
  [:div {:class "flex items-center justify-center h-full"}
   [:span
    {:class "text-gray-400 text-xl"}
    "No template selected"]])

(defn main []
  (fn [{:keys [runbook selected-connections preselected-connection only-runbooks?]}]
    [:<>
     (when-not only-runbooks?
       [:div {:class "absolute right-4 top-4 transition cursor-pointer z-10"
              :on-click #(rf/dispatch [:runbooks-plugin->clear-active-runbooks])}
        [:> hero-micro-icon/XMarkIcon {:class "h-5 w-5 text-[--gray-12]" :aria-hidden "true"}]])
     [template-view (:status runbook) runbook selected-connections preselected-connection]]))

