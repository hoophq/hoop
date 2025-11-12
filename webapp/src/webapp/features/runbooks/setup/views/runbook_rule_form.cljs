(ns webapp.features.runbooks.setup.views.runbook-rule-form
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text]]
   ["lucide-react" :refer [ArrowLeft]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multiselect]
   [webapp.components.connections-select :as connections-select]))

(defn- array->select-options [items]
  (mapv (fn [item]
          {:value item :label item})
        items))

(defn- js-select-options->list [options]
  (mapv #(get % "value") (js->clj options)))

(defn- create-form-state [initial-data]
  (let [rule-data (or initial-data {})]
    {:id (r/atom (or (:id rule-data) ""))
     :name (r/atom (or (:name rule-data) ""))
     :description (r/atom (or (:description rule-data) ""))
     :connection-names (r/atom (or (:connections rule-data) []))
     :user-groups (r/atom (or (array->select-options (:user_groups rule-data)) []))
     :runbooks (r/atom (or (array->select-options (:runbooks rule-data)) []))})) ;; TODO: Get this from the new endpoint

(defn rule-form [form-type rule-data scroll-pos]
  (let [state (create-form-state rule-data)
        is-submitting (r/atom false)]

    (fn []
      (let [connections (rf/subscribe [:connections->pagination])
            user-groups (rf/subscribe [:user-groups])
            selected-connection-names @(:connection-names state)
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
            runbooks-value @(:runbooks state)
            runbooks-input (r/atom "")]
        [:> Box {:class "min-h-screen bg-gray-1"}
         [:form {:id "runbook-rule-form"
                 :on-submit (fn [e]
                              (.preventDefault e)
                              (reset! is-submitting true)
                              (let [payload {:name @(:name state)
                                             :description @(:description state)
                                             :connections @(:connection-names state)
                                             :user_groups (js-select-options->list @(:user-groups state))
                                             :runbooks (js-select-options->list @(:runbooks state))}
                                    final-payload (if (= :edit form-type)
                                                    (assoc payload :id @(:id state))
                                                    payload)]
                                (if (= :edit form-type)
                                  (rf/dispatch [:runbooks-rules/update @(:id state) final-payload])
                                  (rf/dispatch [:runbooks-rules/create final-payload]))
                                (js/setTimeout
                                 #(do
                                    (rf/dispatch [:navigate :runbooks-setup])
                                    (rf/dispatch [:show-snackbar
                                                  {:level :success
                                                   :text (str "Runbook rule "
                                                              (if (= :edit form-type) "updated" "created")
                                                              " successfully!")}]))
                                 1000)))}

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
                "Edit Runbooks Rule"
                "Create new Runbooks rule")]
             [:> Flex {:gap "5" :align "center"}
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
             [multiselect/creatable-select
              {:label "User Groups"
               :options user-group-options
               :default-value @(:user-groups state)
               :on-change #(reset! (:user-groups state) (js->clj %))}]]]

           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "medium"} "Available Runbooks"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Select which Runbooks or paths are available with this rule."]]

            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [multiselect/text-input
              {:label "Runbooks (Optional)"
               :label-description "Use @ for repos and / for folders. Optional, defaults to all Runbooks."
               :value runbooks-value
               :input-value runbooks-input
               :on-change #(reset! (:runbooks state) (js->clj %))
               :on-input-change #(reset! runbooks-input %)}]]]]]]))))

(defn- loading []
  [:> Box {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:> Box {:class "flex items-center justify-center h-full"}
    [:> Text {:size "3" :class "text-[--gray-11]"}
     "Loading..."]]])

(defn main [form-type & [params]]
  (let [runbooks-rules-active (rf/subscribe [:runbooks-rules/active-rule])
        scroll-pos (r/atom 0)]

    (rf/dispatch [:connections/get-connections-paginated {:force-refresh? true}])
    (rf/dispatch [:users->get-user-groups])

    (when (and (= :edit form-type) (:rule-id params))
      (rf/dispatch [:runbooks-rules/get-by-id (:rule-id params)]))

    (fn []
      (r/with-let [handle-scroll #(reset! scroll-pos (.-scrollY js/window))]
        (.addEventListener js/window "scroll" handle-scroll)

        (if (and (= :edit form-type)
                 (= :loading (:status @runbooks-rules-active)))
          [loading]
          [rule-form form-type
           (if (= :edit form-type)
             (:data @runbooks-rules-active)
             {})
           scroll-pos])

        (finally
          (.removeEventListener js/window "scroll" handle-scroll)
          (when (= :edit form-type)
            (rf/dispatch [:runbooks-rules/clear-active-rule])))))))

