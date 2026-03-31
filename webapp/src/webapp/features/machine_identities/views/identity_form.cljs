(ns webapp.features.machine-identities.views.identity-form
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Grid Heading Text]]
   ["lucide-react" :refer [Info]]
   [clojure.string :as str]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.button :as button]
   [webapp.components.connections-select :as connections-select]
   [webapp.components.forms :as forms]
   [webapp.components.loaders :as loaders]
   [webapp.components.multiselect :as multiselect]))

(defn- sanitize-identity-name [value]
  (-> (str value)
      (str/replace #"\s+" "-")
      (str/replace #"[^A-Za-z0-9_.\-]" "")))

(defn- array->select-options [items]
  (mapv (fn [item]
          {:value item :label item})
        items))

(defn- connection-names-from [identity-data]
  (vec (or (:connection-names identity-data)
           (when-let [n (:connection-name identity-data)]
             [n])
           [])))

(defn- create-form-state [initial-data]
  (let [identity-data (or initial-data {})]
    {:identity-name (r/atom (or (:name identity-data) ""))
     :description (r/atom (or (:description identity-data) ""))
     :type (r/atom (or (:type identity-data) "generic"))
     :connection-names (r/atom (connection-names-from identity-data))
     :attributes (r/atom (or (array->select-options (:attributes identity-data)) []))}))

(defn- form-section [{:keys [title description]} & children]
  [:> Grid {:columns "7" :gap "7"}
   [:> Box {:grid-column "span 2 / span 2"}
    [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
     title]
    [:> Text {:size "3" :class "text-[--gray-11]"}
     description]]
   (into [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}]
         children)])

(defn identity-form [form-type identity-data scroll-pos]
  (let [state (create-form-state identity-data)
        connections (rf/subscribe [:connections->pagination])
        current-identity (rf/subscribe [:machine-identities/current-identity])]

    (when (and (= form-type :edit) (nil? @current-identity))
      (rf/dispatch [:machine-identities/get-identity (:id identity-data)]))

    (fn []
      (let [identity-id (when (= form-type :edit) (:id identity-data))
            all-resource-roles (or (:data @connections) [])]

        [:> Box {:class "min-h-screen bg-gray-1"}
         [:form {:on-submit (fn [e]
                              (.preventDefault e)
                              (let [identity-form-data {:name @(:identity-name state)
                                                        :description @(:description state)
                                                        :type @(:type state)
                                                        :connection-names @(:connection-names state)
                                                        :attributes (mapv :value @(:attributes state))}]
                                (if (= form-type :create)
                                  (rf/dispatch [:machine-identities/create identity-form-data])
                                  (rf/dispatch [:machine-identities/update identity-id identity-form-data]))))}

          [:<>
           [:> Flex {:p "5" :gap "2"}
            [button/HeaderBack]]

           [:> Box {:class (str "sticky top-0 z-50 bg-gray-1 px-7 py-7 "
                                (when (>= @scroll-pos 30)
                                  "border-b border-[--gray-a6]"))}
            [:> Flex {:justify "between" :align "center"}
             [:> Heading {:as "h2" :size "8"}
              (if (= form-type :create)
                "Create new Machine Identity"
                "Edit Machine Identity")]
             [:> Flex {:gap "5" :align "center"}
              (when (= form-type :edit)
                [:> Button {:size "4"
                            :variant "ghost"
                            :color "red"
                            :type "button"
                            :on-click #(when (js/confirm (str "Are you sure you want to delete '" @(:identity-name state) "'? This action cannot be undone."))
                                         (rf/dispatch [:machine-identities/delete identity-id])
                                         (rf/dispatch [:navigate :machine-identities]))}
                 "Delete"])
              [:> Button {:size "3"
                          :type "submit"}
               "Save"]]]]

           [:> Box {:p "7" :class "space-y-radix-9"}

            [form-section {:title "Set machine identity information"
                           :description "Used to identify this machine identity across your infrastructure."}
             [forms/input
              (cond-> {:label "Name"
                       :value @(:identity-name state)
                       :required true
                       :class "w-full"}
                (= form-type :create) (assoc :placeholder "e.g. Machine 1"
                                             :autoFocus true
                                             :on-change #(reset! (:identity-name state)
                                                                 (sanitize-identity-name (-> % .-target .-value))))
                (= form-type :edit) (assoc :disabled true))]
             [forms/textarea
              {:label "Description (Optional)"
               :placeholder "Describe who this is used in your connections"
               :value @(:description state)
               :rows 3
               :on-change #(reset! (:description state) (-> % .-target .-value))}]]

            [:> Grid {:columns "7" :gap "7"}
             [:> Box {:grid-column "span 2 / span 2" :class "space-y-radix-4"}
              [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
               "Roles configuration"]
              [:> Text {:size "3" :class "text-[--gray-11]"}
               "Select which Roles to apply this configuration."]
              [:> Callout.Root {:size "1" :color "gray" :variant "surface"}
               [:> Callout.Icon [:> Info {:size 16}]]
               [:> Callout.Text "Roles requiring review aren't available."]]]
             [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
              (let [resource-role-by-name (into {} (map (juxt :name identity)) all-resource-roles)
                    selected-names @(:connection-names state)
                    selected-resource-roles-data
                    (mapv (fn [name]
                            (let [resource-role (get resource-role-by-name name)]
                              {:id (or (:id resource-role) name)
                               :name name}))
                          selected-names)
                    resource-role-ids
                    (mapv (fn [name]
                            (or (:id (get resource-role-by-name name)) name))
                          selected-names)]
                [connections-select/main
                 {:id "machine-identity-resources-input"
                  :name "machine-identity-resources-input"
                  :required? true
                  :label "Resources"
                  :placeholder "Select resources..."
                  :connection-ids resource-role-ids
                  :selected-connections selected-resource-roles-data
                  :on-connections-change
                  (fn [selected-options]
                    (let [selected-js-options (js->clj selected-options :keywordize-keys true)
                          names (mapv :label selected-js-options)]
                      (reset! (:connection-names state) names)))}])]]

            [form-section {:title "Attributes configuration"
                           :description "Select which Attributes to apply this configuration."}
             [multiselect/creatable-select
              {:label "Attributes"
               :placeholder "Type to add attributes..."
               :default-value @(:attributes state)
               :on-change (fn [selected]
                            (reset! (:attributes state) (vec (js->clj selected :keywordize-keys true))))}]]]]]]))))

(defn main [mode & [params]]
  (let [scroll-pos (r/atom 0)
        handle-scroll (fn []
                        (reset! scroll-pos (.-scrollY js/window)))]

    (when (= :edit mode)
      (rf/dispatch [:machine-identities/get-identity (:identity-id params)]))

    (rf/dispatch [:connections/get-connections-paginated {:page 1 :force-refresh? true}])

    (.addEventListener js/window "scroll" handle-scroll)

    (fn []
      (try
        (let [current-identity @(rf/subscribe [:machine-identities/current-identity])
              loading? (and (= :edit mode) (nil? current-identity))]
          (if loading?
            [:> Box {:class "bg-gray-1 h-screen"}
             [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
              [loaders/simple-loader]]]
            [identity-form mode (if (= :edit mode) current-identity nil) scroll-pos]))
        (finally
          (.removeEventListener js/window "scroll" handle-scroll)
          (rf/dispatch [:machine-identities/clear-current-identity]))))))
