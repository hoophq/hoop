(ns webapp.features.runbooks.setup.views.runbook-rule-form
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text]]
   ["lucide-react" :refer [ArrowLeft]]
   [clojure.string :as str]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multiselect]
   [webapp.components.connections-select :as connections-select]
   [webapp.features.runbooks.helpers :refer [extract-repo-name]]))

(defn- array->select-options [items]
  (mapv (fn [item]
          {:value item :label item})
        items))

(defn- js-select-options->list [options]
  (->> options
       (mapv (fn [option]
               (let [option-map (if (map? option)
                                  option
                                  (js->clj option :keywordize-keys true)) ;; Handle both keyword and string keys for compatibility
                     value (or (:value option-map)
                               (get option-map "value"))]
                 value)))
       (filter some?)))


(defn- extract-folder
  "Extract folder from runbook name (e.g., 'ops/update-user.runbook.sh' -> 'ops/')"
  [name]
  (if (string? name)
    (let [parts (str/split name #"/")]
      (if (> (count parts) 1)
        (str (first parts) "/")
        ""))
    ""))

(defn- generate-runbook-options
  "Generate select options from runbooks list in format @repo-name/path for both folders and files"
  [runbooks-list]
  (if (and runbooks-list (= :success (:status runbooks-list)))
    (let [repositories (or (:data runbooks-list) [])]
      (reduce (fn [acc repo-data]
                (let [repository (:repository repo-data)
                      repo-name (extract-repo-name repository)
                      items (or (:items repo-data) [])]
                  (reduce (fn [acc2 item]
                            (let [item-name (:name item)
                                  folder (extract-folder item-name)
                                  ;; Create folder option (if folder exists and not already added)
                                  folder-option (when (seq folder)
                                                  (let [folder-label (str "@" repo-name "/" folder)
                                                        folder-value {:repository repository
                                                                      :name folder
                                                                      :label folder-label}]
                                                    (when (not (some #(= (:label %) folder-label) acc2))
                                                      {:value folder-value :label folder-label})))
                                  ;; Create file option
                                  file-label (str "@" repo-name "/" item-name)
                                  file-value {:repository repository
                                              :name item-name
                                              :label file-label}
                                  file-option {:value file-value :label file-label}]
                              (cond-> acc2
                                folder-option (conj folder-option)
                                true (conj file-option))))
                          acc
                          items)))
              []
              repositories))
    []))

(defn- runbook-options->api-format
  "Convert selected runbook options to API format"
  [selected-options]
  (when selected-options
    (->> selected-options
         (filter some?)
         (mapv (fn [option]
                 (let [value (cond
                               (map? option) (:value option)
                               (aget option "value") (aget option "value")
                               :else nil)
                       value-map (cond
                                   (map? value) value
                                   (some? value) (js->clj value :keywordize-keys true)
                                   :else nil)]
                   (when (and value-map (map? value-map))
                     {:name (or (:name value-map) (get value-map "name"))
                      :repository (or (:repository value-map) (get value-map "repository"))}))))
         (filter some?))))

(defn- api-runbooks->display-format
  "Convert API runbooks format to display options"
  [runbooks]
  (if (seq runbooks)
    (mapv (fn [runbook]
            (let [repository (:repository runbook)
                  name (:name runbook)
                  repo-name (extract-repo-name repository)
                  label (str "@" repo-name "/" name)]
              {:value {:repository repository
                       :name name
                       :label label}
               :label label}))
          runbooks)
    []))

(defn- create-form-state [initial-data]
  (let [rule-data (or initial-data {})]
    {:id (r/atom (or (:id rule-data) ""))
     :name (r/atom (or (:name rule-data) ""))
     :description (r/atom (or (:description rule-data) ""))
     :connection-names (r/atom (or (:connections rule-data) []))
     :user-groups (r/atom (or (array->select-options (:user_groups rule-data)) []))
     :runbooks (r/atom (or (api-runbooks->display-format (:runbooks rule-data)) []))}))

(defn rule-form [form-type rule-data scroll-pos]
  (let [state (create-form-state rule-data)
        is-submitting (r/atom false)
        connections (rf/subscribe [:connections->pagination])
        user-groups (rf/subscribe [:user-groups])
        runbooks-list (rf/subscribe [:runbooks/list])]
    (fn []
      (let [selected-connection-names @(:connection-names state)
            selected-connections-data (mapv (fn [name]
                                              (let [conn (first (filter #(= (:name %) name) (or (:data @connections) [])))]
                                                {:id (or (:id conn) name)
                                                 :name name}))
                                            selected-connection-names)
            connection-ids (mapv (fn [name]
                                   (let [conn (first (filter #(= (:name %) name) (or (:data @connections) [])))]
                                     (or (:id conn) name)))
                                 selected-connection-names)
            user-group-options (array->select-options @user-groups)
            runbook-options (generate-runbook-options @runbooks-list)]
        [:> Box {:class "min-h-screen bg-gray-1"}
         [:form {:id "runbook-rule-form"
                 :on-submit (fn [e]
                              (.preventDefault e)
                              (reset! is-submitting true)
                              (let [payload {:name @(:name state)
                                             :description @(:description state)
                                             :connections @(:connection-names state)
                                             :user_groups (js-select-options->list @(:user-groups state))
                                             :runbooks (runbook-options->api-format @(:runbooks state))}
                                    final-payload (if (= :edit form-type)
                                                    (assoc payload :id @(:id state))
                                                    payload)]
                                (if (= :edit form-type)
                                  (rf/dispatch [:runbooks-rules/update @(:id state) final-payload])
                                  (rf/dispatch [:runbooks-rules/create final-payload]))))}

          [:> Box
           [:> Flex {:p "5" :gap "2"}
            [:> Button {:variant "ghost"
                        :size "2"
                        :color "gray"
                        :type "button"
                        :on-click #(rf/dispatch [:navigate :runbooks-setup])}
             [:> ArrowLeft {:size 16}]
             "Back"]]
           [:> Box {:class (str "sticky top-0 z-50 bg-gray-1 px-7 py-7 "
                                (when (>= @scroll-pos 30)
                                  "border-b border-[--gray-a6]"))}
            [:> Flex {:justify "between"
                      :align "center"}
             [:> Heading {:as "h2" :size "8"}
              (if (= :edit form-type)
                "Edit Runbooks rule"
                "Create new Runbooks rule")]
             [:> Flex {:gap "5" :align "center"}
              (when (= :edit form-type)
                [:> Button {:size "4"
                            :variant "ghost"
                            :color "red"
                            :type "button"
                            :disabled @is-submitting
                            :on-click #(rf/dispatch [:runbooks-rules/delete @(:id state)])}
                 "Delete"])
              [:> Button {:size "3"
                          :loading @is-submitting
                          :disabled @is-submitting
                          :type "submit"}
               "Save"]]]]]

          [:> Box {:p "7" :class "space-y-radix-9"}
           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "medium"} "Set rule information"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Used to identify the rule in your environment."]]

            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [forms/input
              {:label "Name"
               :placeholder "e.g. operations-global"
               :required true
               :value @(:name state)
               :on-change #(reset! (:name state) (-> % .-target .-value))}]
             [forms/input
              {:label "Description (Optional)"
               :placeholder "Describe how this is used in your connections"
               :required false
               :value @(:description state)
               :on-change #(reset! (:description state) (-> % .-target .-value))}]]]

           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "medium"} "Resource configuration"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Select which connections to apply this configuration."]]

            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [connections-select/main
              {:connection-ids connection-ids
               :selected-connections selected-connections-data
               :on-connections-change (fn [selected-options]
                                        (let [selected-js-options (js->clj selected-options :keywordize-keys true)
                                              selected-names (mapv #(:label %) selected-js-options)]
                                          (reset! (:connection-names state) selected-names)))}]]]

           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "medium"} "Group access configuration"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Select which user groups are able to interact with this rule."]]

            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [multiselect/main
              {:label "User Groups"
               :options user-group-options
               :default-value @(:user-groups state)
               :on-change #(reset! (:user-groups state) (js->clj % :keywordize-keys true))}]]]

           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "medium"} "Available Runbooks"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Select which Runbooks or paths are available with this rule."]]

            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [multiselect/main
              {:label "Runbooks (Optional)"
               :options (clj->js runbook-options)
               :default-value (clj->js @(:runbooks state))
               :on-change #(reset! (:runbooks state) (js->clj % :keywordize-keys true))}]]]]]]))))

 (defn loading-view []
  [:> Flex {:justify "center" :align "center" :class "rounded-lg border bg-white h-full"}
   [loaders/simple-loader {:size "6" :border-size "4"}]])

(defn main [form-type & [params]]
  (let [runbooks-rules-active (rf/subscribe [:runbooks-rules/active-rule])
        scroll-pos (r/atom 0)]

    (rf/dispatch [:connections/get-connections-paginated {:force-refresh? true}])
    (rf/dispatch [:users->get-user-groups])
    (rf/dispatch [:runbooks/list])

    (when (and (= :edit form-type) (:rule-id params))
      (rf/dispatch [:runbooks-rules/get-by-id (:rule-id params)]))

    (fn []
      (r/with-let [handle-scroll #(reset! scroll-pos (.-scrollY js/window))]
        (.addEventListener js/window "scroll" handle-scroll)

        (if (and (= :edit form-type)
                 (= :loading (:status @runbooks-rules-active)))
          [loading-view]
          [rule-form form-type
           (if (= :edit form-type)
             (:data @runbooks-rules-active)
             {})
           scroll-pos])

        (finally
          (.removeEventListener js/window "scroll" handle-scroll)
          (when (= :edit form-type)
            (rf/dispatch [:runbooks-rules/clear-active-rule])))))))

