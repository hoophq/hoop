(ns webapp.features.machine-identities.views.identity-roles
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading IconButton Tabs Text TextField]]
   ["lucide-react" :refer [ArrowLeft ChevronDown ChevronUp RefreshCw Search]]
   [clojure.string :as string]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.attribute-filter :as attribute-filter]
   [webapp.components.button :as button]
   [webapp.components.filtered-empty-state :refer [filtered-empty-state]]
   [webapp.components.loaders :as loaders]
   [webapp.components.resource-role-filter :as resource-role-filter]
   [webapp.connections.constants :as connection-constants]))

(defn- credential-field [label value]
  (when (and value (not (string/blank? (str value))))
    [:> Box {:class "space-y-1"}
     [:> Text {:as "label" :size "2" :weight "medium" :class "text-[--gray-12]"}
      label]
     [:> Box {:class "rounded-md bg-[--gray-12] px-3 py-2"}
      [:> Text {:size "2" :class "font-mono text-white break-all"}
       (str value)]]]))

(defn- credential-tabs [_cred]
  (let [active-tab (r/atom "credentials")]
    (fn [cred]
      (let [{:keys [connection_type username password hostname port database_name
                    connection_string aws_access_key_id aws_secret_access_key
                    proxy_token secret_key endpoint_url command]} cred
            aws? (= connection_type "aws")
            has-db-fields? (and hostname (not (string/blank? hostname)))]
        [:> Tabs.Root {:value @active-tab
                       :on-value-change #(reset! active-tab %)}
         [:> Tabs.List {:mb "4"}
          [:> Tabs.Trigger {:value "credentials"} "Credentials"]
          (when connection_string
            [:> Tabs.Trigger {:value "connection-uri"} "Connection URI"])]
         [:> Tabs.Content {:value "credentials"}
          [:> Grid {:columns "2" :gap "4"}
           (cond
             aws?
             [:<>
              [credential-field "Access Key ID" aws_access_key_id]
              [credential-field "Secret Access Key" aws_secret_access_key]
              [credential-field "Endpoint URL" endpoint_url]]

             has-db-fields?
             [:<>
              [credential-field "Database Name" database_name]
              [credential-field "Host" hostname]
              [credential-field "Username" username]
              [credential-field "Password" password]
              [credential-field "Port" port]]

             :else
             [:<>
              [credential-field "Username" username]
              [credential-field "Password" password]
              [credential-field "Proxy Token" proxy_token]
              [credential-field "Secret Key" secret_key]
              [credential-field "Endpoint URL" endpoint_url]
              [credential-field "Command" command]])]]
         (when connection_string
           [:> Tabs.Content {:value "connection-uri"}
            [credential-field "URI" connection_string]])]))))

(defn- credential-row []
  (let [expanded? (r/atom false)
        rotating? (r/atom false)]
    (fn [{:keys [identity-name credential]}]
      (let [{:keys [connection_name connection_type connection_subtype]} credential
            conn-mock {:type connection_type :subtype connection_subtype}]
        [:> Box {:class (str "first:rounded-t-6 last:rounded-b-6 overflow-hidden "
                             "border-[--gray-a6] border-x border-t last:border-b bg-white "
                             (when @expanded? "bg-[--accent-2]"))}
         [:> Box {:p "5" :class "flex justify-between items-center gap-4"}
          [:> Flex {:align "center" :gap "4" :class "min-w-0 flex-1"}
           [:figure {:class "shrink-0 w-9"}
            [:img {:src (or (connection-constants/get-connection-icon conn-mock "rounded")
                            "/icons/database.svg")
                   :class "w-9 h-9"
                   :alt ""}]]
           [:> Text {:size "3" :weight "medium" :class "text-[--gray-12] truncate"}
            connection_name]]
          [:> Flex {:align "center" :gap "2" :class "shrink-0"}
           [:> Button {:size "2"
                       :variant "soft"
                       :color "gray"
                       :disabled @rotating?
                       :on-click (fn []
                                   (reset! rotating? true)
                                   (rf/dispatch [:machine-identities/rotate-credential
                                                 {:identity-name identity-name
                                                  :connection-name connection_name
                                                  :on-complete #(reset! rotating? false)}]))}
            [:> RefreshCw {:size 14
                           :class (when @rotating? "animate-spin")}]
            "Rotate"]
           [:> IconButton {:size "2"
                           :variant "ghost"
                           :color "gray"
                           :aria-expanded (boolean @expanded?)
                           :on-click #(swap! expanded? not)}
            (if @expanded?
              [:> ChevronUp {:size 18}]
              [:> ChevronDown {:size 18}])]]]
         (when @expanded?
           [:> Box {:px "5" :pb "5" :pt "2"
                    :class (str "border-t border-[--gray-a6] bg-[--gray-2] transition-opacity duration-300 "
                                (when @rotating? "opacity-40 pointer-events-none"))}
            [credential-tabs credential]])]))))

(defn- roles-toolbar [{:keys [search-q selected-connection selected-attribute
                              on-search on-select-connection on-clear-connection
                              on-select-attribute on-clear-attribute]}]
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
    [attribute-filter/main {:selected @selected-attribute
                            :on-select on-select-attribute
                            :on-clear on-clear-attribute
                            :label "Attributes"}]
    (when (or (seq (string/trim @search-q))
              @selected-connection
              @selected-attribute)
      [:> Button {:size "2"
                  :variant "soft"
                  :color "gray"
                  :on-click (fn []
                              (on-search "")
                              (on-clear-connection)
                              (on-clear-attribute))}
       "Clear Filters"])]])

(defn main [{:keys [identity-name]}]
  (rf/dispatch [:machine-identities/get-identity identity-name])
  (rf/dispatch [:machine-identities/list-credentials identity-name])

  (let [identity (rf/subscribe [:machine-identities/current-identity])
        credentials (rf/subscribe [:machine-identities/credentials])
        creds-status (rf/subscribe [:machine-identities/credentials-status])
        search-q (r/atom "")
        selected-connection (r/atom nil)
        selected-attribute (r/atom nil)]

    (fn [{:keys [identity-name]}]
      (let [id-data @identity
            loading? (= :loading @creds-status)
            all-credentials @credentials
            q-lower (string/lower-case (string/trim @search-q))
            filtered-credentials
            (cond->> all-credentials
              (not (string/blank? q-lower))
              (filterv #(string/includes?
                         (string/lower-case (or (:connection_name %) ""))
                         q-lower))
              @selected-connection
              (filterv #(= (:connection_name %) @selected-connection))
              @selected-attribute
              (filterv #(some #{@selected-attribute} (:attributes %))))]

        [:> Box {:class "min-h-screen bg-gray-1"}
         [:> Flex {:p "5" :gap "2"}
          [button/HeaderBack]]

         [:> Box {:class "sticky top-0 z-50 bg-gray-1 px-7 py-7"}
          [:> Flex {:justify "between" :align "start" :gap "4" :wrap "wrap"}
           [:> Box {:class "space-y-1 min-w-0"}
            [:> Heading {:as "h2" :size "8"}
             (or (:name id-data) identity-name)]
            (when (:description id-data)
              [:> Text {:size "5" :class "text-[--gray-11]"}
               (:description id-data)])]
           [:> Button {:size "3"
                       :on-click #(rf/dispatch [:navigate :machine-identities-edit {} :identity-name identity-name])}
            "Configure"]]]

         [:> Box {:p "7" :class "space-y-radix-6"}
          (cond
            loading?
            [:> Flex {:direction "column" :justify "center" :align "center" :class "min-h-[240px]"}
             [loaders/simple-loader]]

            (empty? all-credentials)
            [:> Box {:class "rounded-6 border border-[--gray-a6] bg-white p-8"}
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "No credentials are provisioned for this machine identity yet."]]

            :else
            [:<>
             [roles-toolbar
              {:search-q search-q
               :selected-connection selected-connection
               :selected-attribute selected-attribute
               :on-search #(reset! search-q %)
               :on-select-connection #(reset! selected-connection %)
               :on-clear-connection #(reset! selected-connection nil)
               :on-select-attribute #(reset! selected-attribute %)
               :on-clear-attribute #(reset! selected-attribute nil)}]

             [:> Box {:class "flex flex-col min-h-[400px]"}
              (if (empty? filtered-credentials)
                [filtered-empty-state {:entity-name "role"
                                       :entity-name-plural "roles"
                                       :message "No roles match the applied filters"
                                       :subtitle "Try adjusting or clearing your filters to explore more roles."}]
                (doall
                 (for [cred filtered-credentials]
                   ^{:key (:connection_name cred)}
                   [credential-row {:identity-name identity-name
                                    :credential cred}])))]])]]))))
