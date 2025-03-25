(ns webapp.connections.views.connection-list
  (:require ["lucide-react" :refer [Wifi EllipsisVertical InfoIcon Tag Server Database Network]]
            ["@radix-ui/themes" :refer [IconButton Box Button DropdownMenu Tooltip
                                        Flex Text Callout Popover]]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.loaders :as loaders]
            [webapp.components.searchbox :as searchbox]
            [webapp.connections.constants :as connection-constants]
            [webapp.connections.views.connection-settings-modal :as connection-settings-modal]
            [webapp.config :as config]
            [webapp.events.connections-filters]))

(defn empty-list-view []
  [:div {:class "pt-x-large"}
   [:figure
    {:class "w-1/6 mx-auto p-regular"}
    [:img {:src (str config/webapp-url "/images/illustrations/pc.svg")
           :class "w-full"}]]
   [:div {:class "px-large text-center"}
    [:div {:class "text-gray-700 text-sm font-bold"}
     "Beep boop, no sessions to look"]
    [:div {:class "text-gray-500 text-xs mb-large"}
     "There's nothing with this criteria"]]])


(defn- loading-list-view []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn aws-connect-sync-callout []
  (let [aws-jobs-running? @(rf/subscribe [:jobs/aws-connect-running?])]
    (when aws-jobs-running?
      [:> Callout.Root {:class "my-4"}
       [:> Callout.Icon
        [:> InfoIcon {:size 16}]]
       [:> Callout.Text
        [:> Text {:weight "bold" :as "span"} "AWS Connect Sync in Progress"]
        [:> Text {:as "span"} " There is an automated process for your connections happening in your hoop.dev environment. Check it later in order to verify."]]])))

(def connection-types
  [{:id "database" :value "database" :label "Database"}
   {:id "server" :value "server" :label "Linux VM or Container"}
   {:id "application" :value "application" :label "Application"}
   {:id "network" :value "network" :label "Network"}])

(def connection-subtypes
  {:database [{:id "postgres" :value "postgres" :label "PostgreSQL"}
              {:id "mysql" :value "mysql" :label "MySQL"}
              {:id "mongodb" :value "mongodb" :label "MongoDB"}
              {:id "mssql" :value "mssql" :label "MSSQL"}
              {:id "oracledb" :value "oracledb" :label "OracleDB"}]
   :server [{:id "ssh" :value "ssh" :label "SSH"}]
   :application [{:id "tcp" :value "tcp" :label "TCP"}
                 {:id "ruby-on-rails" :value "ruby-on-rails" :label "Ruby on Rails"}
                 {:id "python" :value "python" :label "Python"}
                 {:id "nodejs" :value "nodejs" :label "Node.js"}
                 {:id "clojure" :value "clojure" :label "Clojure"}]
   :network [{:id "vpn" :value "vpn" :label "VPN"}]})

(defn tag-selector-component [selected-tag on-change]
  [:> Popover.Root
   [:> Popover.Trigger {:asChild true}
    [:> Button {:size "2"
                :variant (if selected-tag "soft" "surface")
                :color (if selected-tag "gray" "gray")}
     [:> Flex {:gap "2" :align "center"}
      [:> Tag {:size 16}]
      "Tags"
      (when selected-tag
        [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-gray-800 ml-1"}
         [:span {:class "text-white text-xs font-bold"}
          "1"]])]]]

   [:> Popover.Content {:size "2" :style {:width "280px"}}
    [:> Box {:class "p-2"}
     [searchbox/main
      {:options []
       :display-key :text
       :variant :small
       :searchable-keys [:text]
       :hide-results-list true
       :placeholder "Search tags"
       :name "tags-search"
       :size :small}]]

    [:> Box {:class "max-h-64 overflow-y-auto p-1"}
     [:> Flex {:direction "column" :gap "1"}
      (for [tag ["KeyA=Value" "BValue=Value" "CKey=Value" "DKey=Value" "EValue=Value"]]
        ^{:key tag}
        [:> Button
         {:size "1"
          :variant (if (= selected-tag tag) "soft" "ghost")
          :class "justify-between"
          :onClick #(on-change (if (= selected-tag tag) nil tag))}
         [:span tag]
         (when (= selected-tag tag)
           [:svg {:class "h-4 w-4" :viewBox "0 0 20 20" :fill "currentColor"}
            [:path {:fill-rule "evenodd"
                    :d "M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z"
                    :clip-rule "evenodd"}]])])]]]])

(defn resource-type-component [selected-type on-change]
  [:> Popover.Root
   [:> Popover.Trigger {:asChild true}
    [:> Button {:size "2"
                :variant (if selected-type "soft" "surface")
                :color (if selected-type "gray" "gray")}
     [:> Flex {:gap "2" :align "center"}
      [:> Database {:size 16}]
      "Resource Type"
      (when selected-type
        [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-gray-800 ml-1"}
         [:span {:class "text-white text-xs font-bold"}
          "1"]])]]]

   [:> Popover.Content {:size "2" :style {:width "280px"}}
    [:> Box {:class "p-2"}
     [searchbox/main
      {:options []
       :display-key :text
       :variant :small
       :searchable-keys [:text]
       :hide-results-list true
       :placeholder "Search types"
       :name "type-search"
       :size :small}]]

    [:> Box {:class "max-h-64 overflow-y-auto p-1"}
     [:> Flex {:direction "column" :gap "1"}
      (for [{:keys [id value label]} connection-types]
        ^{:key id}
        [:> Button
         {:size "1"
          :variant (if (= selected-type value) "soft" "ghost")
          :class "justify-between"
          :onClick #(on-change (if (= selected-type value) nil value))}
         [:span label]
         (when (= selected-type value)
           [:svg {:class "h-4 w-4" :viewBox "0 0 20 20" :fill "currentColor"}
            [:path {:fill-rule "evenodd"
                    :d "M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z"
                    :clip-rule "evenodd"}]])])]]]])

(defn resource-subtype-component [selected-type selected-subtype on-change]
  (let [subtypes (get connection-subtypes (keyword selected-type) [])]
    [:> Popover.Root
     [:> Popover.Trigger {:asChild true}
      [:> Button {:size "2"
                  :variant (if selected-subtype "soft" "surface")
                  :color (if selected-subtype "gray" "gray")
                  :disabled (empty? subtypes)}
       [:> Flex {:gap "2" :align "center"}
        [:> Server {:size 16}]
        "Resource Subtype"
        (when selected-subtype
          [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-gray-800 ml-1"}
           [:span {:class "text-white text-xs font-bold"}
            "1"]])]]]

     [:> Popover.Content {:size "2" :style {:width "280px"}}
      [:> Box {:class "p-2"}
       [searchbox/main
        {:options []
         :display-key :text
         :variant :small
         :searchable-keys [:text]
         :hide-results-list true
         :placeholder "Search subtypes"
         :name "subtype-search"
         :size :small}]]

      [:> Box {:class "max-h-64 overflow-y-auto p-1"}
       [:> Flex {:direction "column" :gap "1"}
        (for [{:keys [id value label]} subtypes]
          ^{:key id}
          [:> Button
           {:size "1"
            :variant (if (= selected-subtype value) "soft" "ghost")
            :class "justify-between"
            :onClick #(on-change (if (= selected-subtype value) nil value))}
           [:span label]
           (when (= selected-subtype value)
             [:svg {:class "h-4 w-4" :viewBox "0 0 20 20" :fill "currentColor"}
              [:path {:fill-rule "evenodd"
                      :d "M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z"
                      :clip-rule "evenodd"}]])])]]]]))

(defn panel [_]
  (let [connections (rf/subscribe [:connections])
        user (rf/subscribe [:users->current-user])
        search-focused (r/atom false)
        searched-connections (r/atom nil)
        searched-criteria-connections (r/atom "")
        connections-search-status (r/atom nil)
        selected-tag (r/atom nil)
        selected-type (r/atom nil)
        selected-subtype (r/atom nil)]
    ;; Initial load with no filters
    (rf/dispatch [:connections->get-connections nil])
    (rf/dispatch [:users->get-user])
    (rf/dispatch [:guardrails->get-all])
    (rf/dispatch [:jobs/start-aws-connect-polling])
    (fn []
      (let [connections-search-results (if (empty? @searched-connections)
                                         (:results @connections)
                                         @searched-connections)]
        [:div {:class "flex flex-col h-full overflow-y-auto"}
         (when (-> @user :data :admin?)
           [:div {:class "absolute top-10 right-4 sm:right-6 lg:top-12 lg:right-10"}
            [:> Button {:on-click (fn [] (rf/dispatch [:navigate :create-connection]))}
             "Add Connection"]])
         [:> Flex {:as "header"
                   :direction "column"
                   :gap "3"
                   :class "mb-4"}


          [:> Flex {:gap "2" :class "mb-2 self-end"}
           [searchbox/main
            {:options (:results @connections)
             :display-key :name
             :searchable-keys [:name :type :subtype :connection_tags :status]
             :on-change-results-cb #(reset! searched-connections %)
             :hide-results-list true
             :placeholder "Search"
             :on-focus #(reset! search-focused true)
             :on-blur #(reset! search-focused false)
             :name "connection-search"
             :on-change #(reset! searched-criteria-connections %)
             :loading? (= @connections-search-status :loading)
             :size :small
             :icon-position "left"}]

           [tag-selector-component @selected-tag
            (fn [tag]
              (reset! selected-tag tag)
              (rf/dispatch [:connections->filter-connections
                            (cond-> {}
                              tag (assoc :tagSelector tag)
                              @selected-type (assoc :type @selected-type)
                              @selected-subtype (assoc :subtype @selected-subtype))]))]

           [resource-type-component @selected-type
            (fn [type]
              (reset! selected-type type)
              (reset! selected-subtype nil)
              (rf/dispatch [:connections->filter-connections
                            (cond-> {}
                              @selected-tag (assoc :tagSelector @selected-tag)
                              type (assoc :type type))]))]

           [resource-subtype-component @selected-type @selected-subtype
            (fn [subtype]
              (reset! selected-subtype subtype)
              (rf/dispatch [:connections->filter-connections
                            (cond-> {}
                              @selected-tag (assoc :tagSelector @selected-tag)
                              @selected-type (assoc :type @selected-type)
                              subtype (assoc :subtype subtype))]))]]

          [aws-connect-sync-callout]]

         (if (and (= :loading (:status @connections)) (empty? (:results @connections)))
           [loading-list-view]

           [:div {:class " h-full overflow-y-auto"}
            [:div {:class "relative h-full overflow-y-auto"}
                ;;  (when (and (= status :loading) (empty? (:data sessions)))
                ;;    [loading-list-view])
             (when (and (empty? (:results  @connections)) (not= (:status @connections) :loading))
               [empty-list-view])

             (if (and (empty? @searched-connections)
                      (> (count @searched-criteria-connections) 0))
               [:div {:class "px-regular py-large text-xs text-gray-700 italic"}
                "No connections with this criteria"]

               (doall
                (for [connection connections-search-results]
                  ^{:key (:id connection)}
                  [:> Box {:class (str "bg-white border border-[--gray-3] first:rounded-t-lg last:rounded-b-lg last:border-t-0 first:border-b-0  "
                                       " text-[--gray-12]"
                                       " p-regular text-xs flex gap-8 justify-between items-center")}
                   [:div {:class "flex truncate items-center gap-regular"}
                    [:div
                     [:figure {:class "w-6"}
                      [:img {:src  (connection-constants/get-connection-icon connection)
                             :class "w-9"}]]]
                    [:div
                     [:> Text {:as "p" :size "3" :weight "medium" :class "text-gray-12"}
                      (:name connection)]
                     [:> Text {:size "1" :class "flex items-center gap-1 text-gray-11"}
                      [:div {:class (str "rounded-full h-[6px] w-[6px] "
                                         (if (= (:status connection) "online")
                                           "bg-green-500"
                                           "bg-red-500"))}]
                      (cs/capitalize (:status connection))]]]

                   [:div {:id "connection-info"
                          :class "flex gap-6 items-center"}

                    [:> DropdownMenu.Root {:dir "rtl"}
                     [:> DropdownMenu.Trigger
                      [:> Button {:size 2 :variant "soft"}
                       "Connect"
                       [:> DropdownMenu.TriggerIcon]]]
                     [:> DropdownMenu.Content
                      [:> DropdownMenu.Item {:on-click
                                             (fn []
                                               false)}
                       "Open in Web Terminal"]
                      (when (or
                             (= "database" (:type connection))
                             (and (= "application" (:type connection))
                                  (= "tcp" (:subtype connection))))
                        [:> DropdownMenu.Item {:on-click #(rf/dispatch [:modal->open {:content [connection-settings-modal/main (:name connection)]
                                                                                      :maxWidth "446px"}])}
                         "Open in Native Client"])]]

                    [:> DropdownMenu.Root {:dir "rtl"}
                     [:> DropdownMenu.Trigger
                      [:> IconButton {:size 1 :variant "ghost" :color "gray"}
                       [:> EllipsisVertical {:size 16}]]]
                     [:> DropdownMenu.Content
                      (when (and (-> @user :data :admin?)
                                 (not (= (:managed_by connection) "hoopagent")))
                        [:> DropdownMenu.Item {:on-click
                                               (fn []
                                                 (rf/dispatch [:plugins->get-my-plugins])
                                                 (rf/dispatch [:navigate :edit-connection {} :connection-name (:name connection)]))}
                         "Configure"])
                      [:> DropdownMenu.Item {:color "red"
                                             :on-click (fn []
                                                         (rf/dispatch [:dialog->open
                                                                       {:title "Delete connection?"
                                                                        :type :danger
                                                                        :text-action-button "Confirm and delete"
                                                                        :action-button? true
                                                                        :text [:> Box {:class "space-y-radix-4"}
                                                                               [:> Text {:as "p"}
                                                                                "This action will instantly remove your access to "
                                                                                (:name connection)
                                                                                " and can not be undone."]
                                                                               [:> Text {:as "p"}
                                                                                "Are you sure you want to delete this connection?"]]
                                                                        :on-success (fn []
                                                                                      (rf/dispatch [:connections->delete-connection (:name connection)])
                                                                                      (rf/dispatch [:modal->close]))}]))}
                       "Delete"]]]]])))]])]))))
