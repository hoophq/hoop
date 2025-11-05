(ns webapp.features.runbooks.runner.views.form
  (:require [clojure.string :as cs]
            ["@radix-ui/themes" :refer [Box Flex Heading Text]]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.forms :as forms]
            [webapp.features.runbooks.runner.views.exec-multiples-runbook-list :as exec-multiples-runbooks-list]
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
  [:> Box
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
  [:> Flex {:class "pt-large flex-col gap-regular items-center"}
   [:> Flex {:class "flex-col items-center text-center"}
    [:> Heading {:class " text-base font-bold"}
     "Error found."]
    [:> Box {:class " text-sm mb-large"}
     error]]])

(defmulti template-view identity)

(defmethod template-view :ready [_ _ _]
  (let [state (r/atom {})
        previous-template-name (r/atom nil)
        update-state #(swap! state assoc %1 %2)]

    (let [form-ref (r/atom nil)
          prev-execute (r/atom false)]
      (fn [_ template selected-connections connection-name]
        ;; TODO: This implementation was made to fix the behavior of defmethod not accepting the re-rendering
        ;; based on its own key.
        (if (nil? (:data template))
          [:> Flex {:class "items-center justify-center h-full"}
             [:> Text {:size "5" :class "text-gray-11"}
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

            (let [handle-submit (fn []
                                   (when (and @form-ref (not (.reportValidity @form-ref))) nil)

                                   (when (or (nil? @form-ref) (.reportValidity @form-ref))
                                     (if (> (count selected-connections) 1)
                                       (let [has-jira-template? (some #(seq (:jira_issue_template_id %))
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
                                                                     (seq (:jira_issue_template_id connection)))
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
                                                          :metadata (-> template :data :metadata)
                                                          :params @state
                                                          :connection-name connection-name}]))))))]

              (let [execute-req-sub (rf/subscribe [:runbooks/execute-trigger])
                    execute? @execute-req-sub]
                (when (and (not= @prev-execute execute?) execute?)
                  (handle-submit)
                  (rf/dispatch [:runbooks/execute-handled]))
                (reset! prev-execute execute?))

              [:> Box {:class "overflow-auto lg:overflow-hidden text-[--gray-12]"}
               [:> Box
                [:form
                 {:ref (fn [el] (reset! form-ref el))
                  :on-submit (fn [e] (.preventDefault e))}
                 [:> Flex {:class "h-10 items-center px-3 py-2 border-b border-gray-3 bg-gray-1"}
                  [:> Heading {:as "h1" :size "3" :class "text-gray-12"}
                   (let [parts (cs/split (-> template :data :name) #"/")
                         file-name (last parts)
                         path (cs/join " / " (butlast parts))]
                     [:> Box
                      [:> Text {:size "1" :class "font-normal text-gray-11"} (when path (str path " / "))]
                      [:> Text {:size "3" :class "font-bold"} file-name]])]]
                 [:> Box {:class "p-3 space-y-6 h-[calc(100vh-40px)]"}
                  [:> Text
                   {:size "1" :class "text-gray-11"}
                   "Fill the params below for this Runbook"]

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

                  (when-let [err (-> template :data :error)]
                    [error-view err])]]]
               (when @exec-multiples-runbooks-list/atom-exec-runbooks-list-open?
                 [exec-multiples-runbooks-list/main (map #(into {} {:connection-name (:name %)
                                                                    :file_name (-> template :data :name)
                                                                    :parameters @state
                                                                    :type (:type %)
                                                                    :subtype (:subtype %)
                                                                    :session-id nil
                                                                    :status :ready})
                                                         selected-connections)])])))))))

(defmethod template-view :loading []
  [:> Flex {:class "items-center justify-center h-full"}
   [:figure {:class "w-8"}
    [:img {:class "animate-spin"
           :src (str config/webapp-url "/icons/icon-loader-circle.svg")}]]])

(defmethod template-view :default []
  [:> Flex {:class "items-center justify-center h-full"}
   [:> Text
    {:size "1" :class "text-gray-11"}
    "Select an available Runbook on your Library to begin"]])

(defn main []
  (fn [{:keys [runbook selected-connections preselected-connection only-runbooks?]}]
    [:<>
     (when-not only-runbooks?
       [:> Box {:class "absolute right-4 top-4 transition cursor-pointer z-10"
              :on-click #(rf/dispatch [:runbooks-plugin->clear-active-runbooks])}])
     [template-view (:status runbook) runbook selected-connections preselected-connection]]))

