(ns webapp.features.runbooks.runner.views.form
  (:require [clojure.string :as cs]
            ["@radix-ui/themes" :refer [Box Flex Heading Text ScrollArea]]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.forms :as forms]
            [webapp.config :as config]
            [webapp.features.runbooks.helpers :refer [extract-repo-name]]))

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
          prev-execute (r/atom false)
          runbooks-list (rf/subscribe [:runbooks/list])]
      (fn [_ template selected-connection]
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
                                    (let [connection selected-connection
                                          has-jira-template? (and connection
                                                                  (seq (:jira_issue_template_id connection)))
                                          jira-integration-enabled? (= (-> @(rf/subscribe [:jira-integration->details])
                                                                           :data
                                                                           :status)
                                                                       "enabled")
                                          runbooks-enabled? (= "enabled" (:access_mode_runbooks connection))]

                                      (cond
                                        (not runbooks-enabled?)
                                        (rf/dispatch [:dialog->open
                                                      {:title "Runbooks access mode is disabled"
                                                       :action-button? false
                                                       :text "Your connection does not have runbooks access mode enabled. Please enable it in the connection settings."}])

                                        (and has-jira-template? jira-integration-enabled?)
                                        (rf/dispatch [:runbooks-plugin/show-jira-form
                                                      {:template-id (:jira_issue_template_id connection)
                                                       :file-name (-> template :data :name)
                                                       :params @state
                                                       :connection-name (:name connection)
                                                       :repository (-> template :data :repository)
                                                       :ref-hash (-> template :data :ref-hash)}])

                                        :else (rf/dispatch [:runbooks/exec
                                                            {:file-name (-> template :data :name)
                                                             :params @state
                                                             :connection-name (:name connection)
                                                             :repository (-> template :data :repository)
                                                             :ref-hash (-> template :data :ref-hash)}])))))]

              (let [execute-req-sub (rf/subscribe [:runbooks/execute-trigger])
                    execute? @execute-req-sub]
                (when (and (not= @prev-execute execute?) execute?)
                  (handle-submit)
                  (rf/dispatch [:runbooks/execute-handled]))
                (reset! prev-execute execute?))

              [:> Box {:class "flex flex-col h-full text-[--gray-12]"}
               [:form
                {:ref (fn [el] (reset! form-ref el))
                 :on-submit (fn [e] (.preventDefault e))
                 :class "flex flex-col h-full"}
                [:> Flex {:class "h-10 items-center px-3 py-2 border-b border-gray-3 bg-gray-1 flex-shrink-0"}
                 [:> Heading {:as "h1" :size "3" :class "text-gray-12"}
                  (let [parts (cs/split (-> template :data :name) #"/")
                        file-name (last parts)
                        path (cs/join " / " (butlast parts))
                        runbook-name (-> template :data :name)
                        repositories (or (:data @runbooks-list) [])
                        repository (or (-> template :data :repository)
                                       (let [repo (first (filter #(some (fn [item] (= (:name item) runbook-name)) (:items %)) repositories))]
                                         (when repo (:repository repo))))
                        repo-name (extract-repo-name repository)]
                    [:> Box
                     [:> Text {:size "1" :class "font-normal text-gray-11"} (str repo-name " / ")]
                     [:> Text {:size "1" :class "font-normal text-gray-11"} (when path (str path " / "))]
                     [:> Text {:size "3" :class "font-bold"} file-name]])]]
                [:> ScrollArea
                 [:> Box {:class "p-3 space-y-6 flex-1"}
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
                    [error-view err])]]]])))))))

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
  (fn [{:keys [runbook selected-connection]}]
    [template-view (:status runbook) runbook selected-connection]))
