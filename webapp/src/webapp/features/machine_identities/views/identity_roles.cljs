(ns webapp.features.machine-identities.views.identity-roles
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading IconButton Text TextField]]
   ["lucide-react" :refer [ArrowLeft ChevronDown ChevronUp RefreshCw Search]]
   [clojure.string :as string]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.filtered-empty-state :refer [filtered-empty-state]]
   [webapp.components.loaders :as loaders]
   [webapp.connections.constants :as connection-constants]))

(defn- credential-field [label value]
  (when (and value (not (string/blank? (str value))))
    [:> Box {:class "space-y-1"}
     [:> Text {:as "label" :size "2" :weight "medium" :class "text-[--gray-12]"}
      label]
     [:> Box {:class "rounded-md bg-[--gray-12] px-3 py-2"}
      [:> Text {:size "2" :class "font-mono text-white break-all"}
       (str value)]]]))

(defn- credential-fields-for-type [cred]
  (let [{:keys [connection_type username password hostname port database_name
                connection_string aws_access_key_id aws_secret_access_key
                proxy_token secret_key endpoint_url command]} cred
        aws? (= connection_type "aws")
        has-db-fields? (and hostname (not (string/blank? hostname)))]
    [:> Box {:class "space-y-4"}
     (cond
       aws?
       [:<>
        [credential-field "Access Key ID" aws_access_key_id]
        [credential-field "Secret Access Key" aws_secret_access_key]
        [credential-field "Endpoint URL" endpoint_url]]

       has-db-fields?
       [:<>
        [credential-field "Host" hostname]
        [credential-field "Port" port]
        [credential-field "Database" database_name]
        [credential-field "Username" username]
        [credential-field "Password" password]
        [credential-field "Connection String" connection_string]]

       :else
       [:<>
        [credential-field "Username" username]
        [credential-field "Password" password]
        [credential-field "Proxy Token" proxy_token]
        [credential-field "Secret Key" secret_key]
        [credential-field "Endpoint URL" endpoint_url]
        [credential-field "Command" command]])]))

(defn- credential-row []
  (let [expanded? (r/atom false)
        rotating? (r/atom false)]
    (fn [{:keys [identity-name credential]}]
      (let [{:keys [connection_name connection_type connection_subtype]} credential
            conn-mock {:type connection_type :subtype connection_subtype}]
        [:> Box {:class (str "first:rounded-t-6 last:rounded-b-6 "
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
                                                  :connection-name connection_name}])
                                   (js/setTimeout #(reset! rotating? false) 3000))}
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
           [:> Box {:px "5" :pb "5" :pt "2" :class "border-t border-[--gray-a6] bg-[--gray-2]"}
            [credential-fields-for-type credential]])]))))

(defn- credentials-toolbar [{:keys [search-q on-search]}]
  [:> Flex {:gap "2" :mb "4" :wrap "wrap" :align "center" :justify "between"}
   [:> Heading {:as "h2" :size "6" :weight "bold" :class "text-[--gray-12]"}
    "Credentials"]
   [:> TextField.Root {:placeholder "Search"
                       :value @search-q
                       :on-change #(on-search (-> % .-target .-value))
                       :class "w-[200px]"}
    [:> TextField.Slot [:> Search {:size 16}]]]])

(defn main [{:keys [identity-name]}]
  (rf/dispatch [:machine-identities/get-identity identity-name])
  (rf/dispatch [:machine-identities/list-credentials identity-name])

  (let [identity (rf/subscribe [:machine-identities/current-identity])
        credentials (rf/subscribe [:machine-identities/credentials])
        creds-status (rf/subscribe [:machine-identities/credentials-status])
        search-q (r/atom "")]

    (fn [{:keys [identity-name]}]
      (let [id-data @identity
            loading? (= :loading @creds-status)
            all-credentials @credentials
            q-lower (string/lower-case (string/trim @search-q))
            filtered-credentials (if (string/blank? q-lower)
                                   all-credentials
                                   (filterv #(string/includes?
                                              (string/lower-case (:connection_name %))
                                              q-lower)
                                            all-credentials))]

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

           :else
           [:<>
            [:> Flex {:justify "between" :align "start" :class "mb-8 gap-4 flex-wrap"}
             [:> Box {:class "space-y-1 min-w-0"}
              [:> Heading {:as "h1" :size "8" :weight "bold" :class "text-[--gray-12]"}
               (or (:name id-data) identity-name)]
              (when (:description id-data)
                [:> Text {:size "5" :class "text-[--gray-11]"}
                 (:description id-data)])]
             [:> Button {:size "3"
                         :variant "soft"
                         :color "blue"
                         :on-click #(rf/dispatch [:navigate :machine-identities-edit {} :identity-name identity-name])}
              "Configure"]]

            (if (empty? all-credentials)
              [:> Box {:class "rounded-6 border border-[--gray-a6] bg-white p-8"}
               [:> Text {:size "3" :class "text-[--gray-11]"}
                "No credentials are provisioned for this machine identity yet."]]

              [:<>
               [credentials-toolbar {:search-q search-q
                                     :on-search #(reset! search-q %)}]

               [:> Box
                (if (empty? filtered-credentials)
                  [filtered-empty-state {:entity-name "credential"
                                         :entity-name-plural "credentials"
                                         :filter-value (when (seq (string/trim @search-q)) @search-q)}]
                  (doall
                   (for [cred filtered-credentials]
                     ^{:key (:connection_name cred)}
                     [credential-row {:identity-name identity-name
                                      :credential cred}])))]])])]))))
