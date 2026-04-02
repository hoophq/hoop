(ns webapp.features.attributes.views.form
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.button :as button]
   [webapp.components.connections-select :as connections-select]
   [webapp.components.forms :as forms]
   [webapp.components.loaders :as loaders]))

(defn- form-section [{:keys [title description]} & children]
  [:> Grid {:columns "7" :gap "7"}
   [:> Box {:grid-column "span 2 / span 2"}
    [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
     title]
    [:> Text {:size "3" :class "text-[--gray-11]"}
     description]]
   (into [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}]
         children)])

(defn- create-form-state [initial-data]
  (let [data (or initial-data {})]
    {:attr-name (r/atom (or (:name data) ""))
     :description (r/atom (or (:description data) ""))
     :connection-names (r/atom (or (:connection_names data) []))}))

(defn- attribute-form [form-type attr-data scroll-pos]
  (let [state (create-form-state attr-data)
        resource-roles (rf/subscribe [:connections->pagination])
        submitting? (rf/subscribe [:attributes/submitting?])]
    (fn []
      (let [attr-name @(:attr-name state)
            all-roles (or (:data @resource-roles) [])
            role-by-name (into {} (map (juxt :name identity)) all-roles)
            selected-names @(:connection-names state)
            selected-roles-data (mapv (fn [n]
                                        (let [role (get role-by-name n)]
                                          {:id (or (:id role) n) :name n}))
                                      selected-names)
            role-ids (mapv (fn [n]
                             (or (:id (get role-by-name n)) n))
                           selected-names)]

        [:> Box {:class "min-h-screen bg-gray-1"}
         [:form {:on-submit (fn [e]
                              (.preventDefault e)
                              (let [body (cond-> {:name attr-name
                                                  :connection_names @(:connection-names state)}
                                           (seq @(:description state))
                                           (assoc :description @(:description state)))]
                                (if (= form-type :create)
                                  (rf/dispatch [:attributes/create body])
                                  (rf/dispatch [:attributes/update attr-name body]))))}

          [:<>
           [:> Flex {:p "5" :gap "2"}
            [button/HeaderBack]]

           [:> Box {:class (str "sticky top-0 z-50 bg-gray-1 px-7 py-7 "
                                (when (>= @scroll-pos 30)
                                  "border-b border-[--gray-a6]"))}
            [:> Flex {:justify "between" :align "center"}
             [:> Heading {:as "h2" :size "8"}
              (if (= form-type :create)
                "Create Attribute"
                "Configure Attribute")]
             [:> Flex {:gap "5" :align "center"}
              (when (= form-type :edit)
                [:> Button {:size "4"
                            :variant "ghost"
                            :color "red"
                            :type "button"
                            :on-click #(rf/dispatch [:dialog->open
                                                     {:title "Delete Attribute"
                                                      :text (str "Are you sure you want to delete the attribute '" attr-name "'? This action cannot be undone.")
                                                      :text-action-button "Delete"
                                                      :action-button? true
                                                      :type :danger
                                                      :on-success (fn []
                                                                    (rf/dispatch [:attributes/delete attr-name]))}])}
                 "Delete"])
              [:> Button {:size "3"
                          :type "submit"
                          :disabled @submitting?}
               "Save"]]]]

           [:> Box {:p "7" :class "space-y-radix-9"}
            [form-section {:title "Set Attribute information"
                           :description "Used to identify Attributes on resources."}
             [forms/input
              (cond-> {:label "Name"
                       :value @(:attr-name state)
                       :required true
                       :class "w-full"}
                (= form-type :create) (assoc :placeholder "e.g. engineering"
                                             :autoFocus true
                                             :on-change #(reset! (:attr-name state) (-> % .-target .-value)))
                (= form-type :edit) (assoc :disabled true))]
             [forms/input
              {:placeholder "Describe how this attribute is used"
               :label "Description (Optional)"
               :value @(:description state)
               :class "w-full"
               :on-change #(reset! (:description state) (-> % .-target .-value))}]]

            [form-section {:title "Role configuration"
                           :description "Select which Roles to apply this configuration."}
             [connections-select/main
              {:id "attribute-connections-input"
               :name "attribute-connections-input"
               :connection-ids role-ids
               :selected-connections selected-roles-data
               :on-connections-change (fn [selected-options]
                                        (let [selected (js->clj selected-options :keywordize-keys true)
                                              names (mapv :label selected)]
                                          (reset! (:connection-names state) names)))}]]]]]]))))

(defn main [form-type]
  (let [active-attr (rf/subscribe [:attributes/active])
        scroll-pos (r/atom 0)]

    (rf/dispatch [:connections/get-connections-paginated {:force-refresh? true}])

    (fn []
      (r/with-let [handle-scroll #(reset! scroll-pos (.-scrollY js/window))
                   _ (.addEventListener js/window "scroll" handle-scroll)]
        (let [attr-data (if (= :edit form-type)
                          (:data @active-attr)
                          {})
              loading? (and (= :edit form-type)
                            (= :loading (:status @active-attr)))]
          (if loading?
            [:> Box {:class "bg-gray-1 h-full"}
             [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
              [loaders/simple-loader]]]
            ^{:key (str form-type "-" (:name attr-data))}
            [attribute-form form-type attr-data scroll-pos]))
        (finally
          (.removeEventListener js/window "scroll" handle-scroll))))))
