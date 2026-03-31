(ns webapp.features.machine-identities.views.identity-roles
  (:require
   ["@radix-ui/themes" :refer [Box Button DropdownMenu Flex Grid Heading IconButton Tabs Text TextField]]
   ["lucide-react" :refer [ArrowLeft ChevronDown ChevronUp Search]]
   [clojure.string :as string]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.filtered-empty-state :refer [filtered-empty-state]]
   [webapp.components.loaders :as loaders]
   [webapp.components.resource-role-filter :as resource-role-filter]
   [webapp.connections.constants :as connection-constants]))

(defn- roles-after-filters
  "Client-side filters for the roles list (search substring, resource role, attributes)."
  [all-roles search-trimmed selected-connection selected-attributes]
  (let [q-lower (string/lower-case search-trimmed)
        by-search (if (string/blank? q-lower)
                    all-roles
                    (filterv #(string/includes?
                               (string/lower-case (:name %))
                               q-lower)
                             all-roles))
        by-conn (if (nil? selected-connection)
                  by-search
                  (filterv #(= (:resource-role %) selected-connection) by-search))]
    (if (empty? selected-attributes)
      (sort-by :name by-conn)
      (sort-by :name (filterv #(some selected-attributes (:attributes %)) by-conn)))))

(defn- role-status-badge [status]
  (let [online? (= status :online)]
    [:> Flex {:align "center" :gap "2"}
     [:span {:class (str "inline-block size-2 shrink-0 rounded-full "
                         (if online? "bg-[--green-9]" "bg-[--gray-8]"))}]
     [:> Text {:size "2" :class "text-[--gray-11]"}
      (if online? "Online" "Offline")]]))

(defn- credential-field [label value]
  [:> Box {:class "space-y-1"}
   [:> Text {:as "label" :size "2" :weight "medium" :class "text-[--gray-12]"}
    label]
   [:> Box {:class "rounded-md bg-[--gray-12] px-3 py-2"}
    [:> Text {:size "2" :class "font-mono text-white break-all"}
     (str value)]]])

(defn- role-expanded-panel [role active-tab]
  (let [c (:credentials role)]
    [:> Box {:px "5" :pb "5" :pt "2" :class "border-t border-[--gray-a6] bg-[--gray-2]"}
     [:> Tabs.Root {:value @active-tab
                    :on-value-change #(reset! active-tab %)}
      [:> Tabs.List {:mb "4"}
       [:> Tabs.Trigger {:value "credentials"} "Credentials"]
       [:> Tabs.Trigger {:value "connection-uri"} "Connection URI"]]
      [:> Tabs.Content {:value "credentials"}
       [:> Grid {:columns "2" :gap "4"}
        [credential-field "Database Name" (:database-name c)]
        [credential-field "Host" (:host c)]
        [credential-field "Username" (:username c)]
        [credential-field "Password" (:password c)]
        [credential-field "Port" (:port c)]]]
      [:> Tabs.Content {:value "connection-uri"}
       [credential-field "URI" (:connection-uri role)]]]]))

(defn- role-row []
  (let [expanded? (r/atom false)
        active-tab (r/atom "credentials")]
    (fn [{:keys [role]}]
      [:> Box {:class (str "first:rounded-t-6 last:rounded-b-6 "
                           "border-[--gray-a6] border-x border-t last:border-b bg-white "
                           (when @expanded? "bg-[--accent-2]"))}
       [:> Box {:p "5" :class "flex justify-between items-center gap-4"}
        [:> Flex {:align "center" :gap "4" :class "min-w-0 flex-1"}
         [:figure {:class "shrink-0 w-9"}
          [:img {:src (or (connection-constants/get-connection-icon (:connection-stub role) "rounded")
                          "/icons/database.svg")
                 :class "w-9 h-9"
                 :alt (str "Connection type for " (:name role))}]]
         [:> Flex {:direction "column" :gap "1" :class "min-w-0 flex-1"}
          [:> Text {:size "3" :weight "medium" :class "text-[--gray-12] truncate"}
           (:name role)]
          [role-status-badge (:status role)]]]
        [:> IconButton {:size "2"
                        :variant "ghost"
                        :color "gray"
                        :aria-expanded (boolean @expanded?)
                        :on-click #(swap! expanded? not)}
         (if @expanded?
           [:> ChevronUp {:size 18}]
           [:> ChevronDown {:size 18}])]]
       (when @expanded?
         [role-expanded-panel role active-tab])])))

(defn- roles-toolbar [{:keys [search-q selected-connection selected-attributes
                              all-attributes on-search on-select-connection on-clear-connection
                              on-toggle-attribute on-clear-filters]}]
  [:> Flex {:gap "2" :mb "4" :wrap "wrap" :align "center" :justify "between"}
   [:> Heading {:as "h2" :size "6" :weight "bold" :class "text-[--gray-12]"}
    "Roles"]
   [:> Flex {:gap "2" :wrap "wrap"}
    [:> TextField.Root {:placeholder "Search"
                        :value @search-q
                        :on-change #(on-search (-> % .-target .-value))
                        :class "w-[200px]"}
     [:> TextField.Slot [:> Search {:size 16}]]]
    [resource-role-filter/main {:selected @selected-connection
                                :on-select on-select-connection
                                :on-clear on-clear-connection
                                :label "Resource Role"}]
    (when (seq all-attributes)
      [:> DropdownMenu.Root
       [:> DropdownMenu.Trigger
        [:> Button {:size "2"
                    :variant (if (seq @selected-attributes) "soft" "surface")
                    :color "gray"
                    :class "gap-2"}
         [:> Text {:size "2" :weight "medium"}
          (if (seq @selected-attributes)
            (str "Attributes (" (count @selected-attributes) ")")
            "Attributes")]]]
       [:> DropdownMenu.Content {:style {:max-height "300px" :overflow-y "auto"}}
        (for [attr all-attributes]
          ^{:key attr}
          [:> DropdownMenu.CheckboxItem
           {:checked (contains? @selected-attributes attr)
            :on-checked-change #(if %
                                  (on-toggle-attribute :add attr)
                                  (on-toggle-attribute :remove attr))}
           attr])]])
    (when (or (seq (string/trim @search-q))
              @selected-connection
              (seq @selected-attributes))
      [:> Button {:size "2"
                  :variant "soft"
                  :color "gray"
                  :on-click on-clear-filters}
       "Clear Filters"])]])

(defn main [{:keys [identity-id]}]
  (let [identity (rf/subscribe [:machine-identities/identity-by-id identity-id])
        list-status (rf/subscribe [:machine-identities/status])
        search-q (r/atom "")
        selected-connection (r/atom nil)
        selected-attributes (r/atom #{})]
    (fn [{:keys [identity-id]}]
      (let [id-data @identity
            loading? (= :loading @list-status)
            all-roles (vec (or (:roles id-data) []))
            processed-roles (roles-after-filters all-roles
                                                 (string/trim @search-q)
                                                 @selected-connection
                                                 @selected-attributes)
            all-role-attrs (->> all-roles
                                (mapcat :attributes)
                                (distinct)
                                (sort))]

        [:> Box {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
         [:> Button {:variant "ghost"
                     :color "gray"
                     :class "mb-4 -ml-2 gap-1"
                     :on-click #(rf/dispatch [:navigate :machine-identities])}
          [:> ArrowLeft {:size 16}]
          "Back"]

         (cond
           loading?
           [:> Flex {:direction "column" :justify "center" :align "center" :class "min-h-[240px]"}
            [loaders/simple-loader]]

           (nil? id-data)
           [:> Box {:class "rounded-6 border border-[--gray-a6] bg-white p-8"}
            [:> Text {:size "3" :class "text-[--gray-11]"}
             "Machine identity not found."]]

           :else
           [:<>
            [:> Flex {:justify "between" :align "start" :class "mb-8 gap-4 flex-wrap"}
             [:> Box {:class "space-y-1 min-w-0"}
              [:> Heading {:as "h1" :size "8" :weight "bold" :class "text-[--gray-12]"}
               (:name id-data)]
              (when (:description id-data)
                [:> Text {:size "5" :class "text-[--gray-11]"}
                 (:description id-data)])]
             [:> Button {:size "3"
                         :variant "soft"
                         :color "blue"
                         :on-click #(rf/dispatch [:navigate :machine-identities-edit {} :identity-id identity-id])}
              "Configure"]]

            (if (empty? all-roles)
              [:> Box {:class "rounded-6 border border-[--gray-a6] bg-white p-8"}
               [:> Text {:size "3" :class "text-[--gray-11]"}
                "No roles are assigned to this machine identity yet."]]

              [:<>
               [roles-toolbar
                {:search-q search-q
                 :selected-connection selected-connection
                 :selected-attributes selected-attributes
                 :all-attributes all-role-attrs
                 :on-search #(reset! search-q %)
                 :on-select-connection #(reset! selected-connection %)
                 :on-clear-connection #(reset! selected-connection nil)
                 :on-toggle-attribute (fn [op attr]
                                        (case op
                                          :add (swap! selected-attributes conj attr)
                                          :remove (swap! selected-attributes disj attr)))
                 :on-clear-filters (fn []
                                     (reset! search-q "")
                                     (reset! selected-connection nil)
                                     (reset! selected-attributes #{}))}]

               [:> Box
                (if (empty? processed-roles)
                  [filtered-empty-state {:entity-name "role"
                                         :entity-name-plural "roles"
                                         :filter-value (or (when (seq (string/trim @search-q)) @search-q)
                                                           @selected-connection
                                                           (when (seq @selected-attributes)
                                                             (str (count @selected-attributes) " attributes")))}]
                  (doall
                   (for [role processed-roles]
                     ^{:key (:id role)}
                     [role-row {:role role}])))]])])]))))
