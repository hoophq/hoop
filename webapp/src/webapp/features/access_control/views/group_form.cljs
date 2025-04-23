(ns webapp.features.access-control.views.group-form
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text Button Heading TextArea]]
   ["@heroicons/react/24/outline" :as hero-outline]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [clojure.string :as str]
   [webapp.components.button :as button]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]))

(defn back-button []
  [:button {:class "inline-flex items-center text-sm text-gray-600 mb-6 hover:text-gray-900"
            :on-click #(rf/dispatch [:navigate :access-control])}
   [:> hero-outline/ArrowLeftIcon {:class "h-4 w-4 mr-1"}]
   "Back"])

(defn create-form []
  (let [group-name (r/atom "")
        description (r/atom "")
        selected-connections (r/atom [])
        all-connections (rf/subscribe [:connections])
        is-submitting (r/atom false)]

    ;; Carregar conexões disponíveis
    (rf/dispatch [:connections->get-connections])

    (fn []
      [:> Box {:class "w-full max-w-3xl mx-auto"}
       [back-button]

       [:> Heading {:size "6" :weight "bold" :class "mb-6"}
        "Create new access control group"]

       [:form {:on-submit (fn [e]
                            (.preventDefault e)
                            (reset! is-submitting true)
                            (let [connection-ids (map #(get % "value") @selected-connections)
                                  selected-conns (filter #(some #{(:id %)} connection-ids) (:results @all-connections))]
                              (rf/dispatch [:access-control/create-group-with-permissions
                                            {:name @group-name
                                             :description @description
                                             :connections selected-conns}])))}

        [:> Box {:class "bg-white rounded-lg shadow-sm p-8 mb-6"}
         [:> Heading {:size "4" :weight "medium" :class "mb-4"}
          "Set group information"]
         [:> Text {:size "2" :class "text-gray-500 mb-6 block"}
          "Used to identify your access control group."]

         ;; Group name field
         [:> Box {:class "mb-6"}
          [:> Text {:as "label" :size "2" :weight "medium" :class "block mb-2"}
           "Name"]
          [forms/input
           {:placeholder "e.g. engineering-team"
            :value @group-name
            :required true
            :class "w-full"
            :autoFocus true
            :disabled @is-submitting
            :on-change #(reset! group-name (-> % .-target .-value))}]]

         ;; Description field (optional)
         [:> Box {:class "mb-6"}
          [:> Flex {:align "baseline" :justify "between" :class "mb-2"}
           [:> Text {:as "label" :size "2" :weight "medium"}
            "Description"]
           [:> Text {:size "1" :class "text-gray-500"}
            "(Optional)"]]
          [:> TextArea {:placeholder "Describe how this group will be used"
                        :value @description
                        :disabled @is-submitting
                        :on-change #(reset! description (-> % .-target .-value))}]]]

        ;; Conexões
        [:> Box {:class "bg-white rounded-lg shadow-sm p-8 mb-6"}
         [:> Heading {:size "4" :weight "medium" :class "mb-4"}
          "Connection permissions"]
         [:> Text {:size "2" :class "text-gray-500 mb-6 block"}
          "Select which connections this group should have access to."]

         [:> Box {:class "mb-6"}
          [:> Text {:as "label" :size "2" :weight "medium" :class "block mb-2"}
           "Connections"]
          [:> Text {:size "2" :class "text-gray-500 mb-4 block"}
           "Select which connections this group can access."]

          [multi-select/main
           {:id "connections-input"
            :name "connections-input"
            :options (mapv #(hash-map "value" (:id %) "label" (:name %))
                           (:results @all-connections))
            :default-value @selected-connections
            :placeholder "Select connections..."
            :on-change #(reset! selected-connections (js->clj %))}]]]

        ;; Action buttons
        [:> Flex {:justify "end" :gap "3"}
         [:> Button {:variant "soft"
                     :type "button"
                     :disabled @is-submitting
                     :onClick #(rf/dispatch [:navigate :access-control])}
          "Cancel"]
         [:> Button {:type "submit"
                     :disabled (or @is-submitting (str/blank? @group-name))
                     :class "bg-blue-600 hover:bg-blue-700"}
          (if @is-submitting
            "Creating..."
            "Create Group")]]]])))

(defn edit-form [group-id]
  (let [connections (rf/subscribe [:connections])
        plugin-details (rf/subscribe [:plugins->plugin-details])
        group-connections (rf/subscribe [:access-control/group-permissions group-id])
        selected-connections (r/atom nil)
        is-submitting (r/atom false)]

    ;; Initialize selected connections when component mounts
    (rf/dispatch [:plugins->get-plugin-by-name "access_control"])
    (rf/dispatch [:connections->get-connections])

    (fn [group-id]
      ;; Quando temos conexões e o plugin carregado, inicializamos as conexões selecionadas
      (when (and (nil? @selected-connections)
                 @connections
                 (seq @group-connections)
                 (not= (:status @plugin-details) :loading))
        (println @group-connections)
        (reset! selected-connections
                (->> @group-connections
                     (map #(hash-map "value" (:id %) "label" (:name %)))
                     vec)))

      [:> Box {:class "w-full max-w-3xl mx-auto"}
       [back-button]

       [:> Heading {:size "6" :weight "bold" :class "mb-6"}
        (str "Edit group: " group-id)]

       [:form {:on-submit (fn [e]
                            (.preventDefault e)
                            (reset! is-submitting true)
                            (let [connection-ids (map #(get % "value") @selected-connections)
                                  selected-conns (filter #(some #{(:id %)} connection-ids) (:results @connections))]
                              (rf/dispatch [:access-control/add-group-permissions
                                            {:group-id group-id
                                             :connections selected-conns
                                             :plugin (:plugin @plugin-details)}])
                              (js/setTimeout #(rf/dispatch [:navigate :access-control]) 1000)))}

        [:> Box {:class "bg-white rounded-lg shadow-sm p-8 mb-6"}
         [:> Heading {:size "4" :weight "medium" :class "mb-4"}
          "Group configuration"]
         [:> Text {:size "2" :class "text-gray-500 mb-6 block"}
          "Select which connections this group should have access to."]

         ;; Group name field (disabled)
         [:> Box {:class "mb-6"}
          [:> Text {:as "label" :size "2" :weight "medium" :class "block mb-2"}
           "Group name"]
          [forms/input
           {:value group-id
            :disabled true
            :class "w-full bg-gray-50"}]]

         ;; Connections selection
         [:> Box {:class "mb-6"}
          [:> Text {:as "label" :size "2" :weight "medium" :class "block mb-2"}
           "Connections"]
          [:> Text {:size "2" :class "text-gray-500 mb-4 block"}
           "Select which connections this group can access."]

          [multi-select/main
           {:id "connections-input"
            :name "connections-input"
            :options (mapv #(hash-map "value" (:id %) "label" (:name %))
                           (:results @connections))
            :default-value @selected-connections
            :placeholder "Select connections..."
            :on-change #(reset! selected-connections (js->clj %))}]]]

        ;; Action buttons
        [:> Flex {:justify "end" :gap "3"}
         [:> Button {:variant "soft"
                     :type "button"
                     :disabled @is-submitting
                     :onClick #(rf/dispatch [:navigate :access-control])}
          "Cancel"]
         [:> Button {:type "submit"
                     :disabled @is-submitting
                     :class "bg-blue-600 hover:bg-blue-700"}
          (if @is-submitting
            "Saving..."
            "Save Changes")]]]])))

(defn main [mode & [params]]
  (case mode
    :create [create-form]
    :edit [edit-form (:group-id params)]
    [:div "Invalid form mode"]))
