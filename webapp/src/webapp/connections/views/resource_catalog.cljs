(ns webapp.connections.views.resource-catalog
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Card Dialog Flex Heading
                               ScrollArea Tabs Text]]
   ["lucide-react" :refer [ExternalLink Check]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

;; Mock data
(def mock-popular-connections #{"aws-cli" "mysql" "postgres" "kubernetes" "mongodb"})
(def mock-new-connections #{"postgres"})
(def mock-beta-connections #{"mongodb"})

(defn get-connection-badge [connection-id]
  (cond
    (mock-new-connections connection-id) {:text "NEW" :color "green"}
    (mock-beta-connections connection-id) {:text "BETA" :color "blue"}
    :else nil))

(defn connection-icon [icon-name connection-id]
  (let [image-failed? (r/atom false)]
    (fn []
      (if @image-failed?
        ;; Show fallback - no more image loading, just CSS
        [:div {:class "w-6 h-6 bg-gradient-to-br from-blue-500 to-blue-600 rounded-md flex items-center justify-center shadow-sm"}
         [:span {:class "text-white font-bold text-sm"}
          (cs/upper-case (first (str connection-id)))]]
        ;; Try to load image
        [:img {:src (str "/icons/connections/" (or icon-name connection-id) "-default.svg")
               :alt connection-id
               :class "w-6 h-6"
               :on-error (fn [_]
                           ;; Only set flag, no more image attempts
                           (reset! image-failed? true))}]))))

(defn connection-card [connection on-click]
  (let [{:keys [id name icon-name]} connection
        badge (get-connection-badge id)]
    [:> Box {:height "110px" :width "165px"}
     [:> Card {:size "2"
               :class "h-full w-full cursor-pointer"
               :on-click #(on-click connection)}
      [:> Flex {:direction "column" :justify "between" :gap "2" :class "h-full w-full"}
       [:> Flex {:align "center" :justify "between" :gap "2"}
        [:> Box
         [connection-icon icon-name id]]

        (when badge
          [:> Badge {:color (:color badge)
                     :size "1"}
           (:text badge)])]

       [:> Text {:size "2" :weight "medium" :align "left" :class "text-[--gray-12]"}
        name]]]]))

(defn category-section [title connections on-connection-click]
  (when (seq connections)
    [:> Box {:class "space-y-radix-5"}
     [:> Heading {:as "h3" :size "5" :weight "bold" :class "mb-6 text-[--gray-12]"}
      title]
     [:> Flex {:direction "row" :wrap "wrap" :gap "4"}
      (for [connection connections]
        ^{:key (:id connection)}
        [connection-card connection on-connection-click])]]))

(defn search-section [search-term on-search-change]
  [:> Box {:class "space-y-radix-4"}
   [:> Text {:size "2" :weight "bold" :class "block text-[--gray-12]"}
    "Search"]
   [:> Box {:class "relative"}
    [forms/input {:placeholder "Resources or keywords"
                  :value search-term
                  :on-change #(on-search-change (.. % -target -value))}]]])

(defn categories-filter [selected-categories on-category-change all-categories]
  [:> Box {:class "space-y-radix-4"}
   [:> Text {:size "2" :weight "bold" :class "block text-[--gray-12]"}
    "Categories"]

   (for [category all-categories]
     ^{:key category}
     [:> Box {:class "flex items-center cursor-pointer space-x-3"
              :on-click #(on-category-change category)}
      [:> Text {:size "2" :class "text-[--gray-12] capitalize"}
       (cs/replace category #"-" " ")]
      (when (contains? selected-categories category)
        [:> Check {:size 16}])])])

(defn tags-filter [selected-tags on-tag-change all-tags]
  [:div {:class "space-y-radix-4"}
   [:> Text {:size "2" :weight "bold" :class "block text-[--gray-12]"}
    "Tags"]
   [:> Flex {:direction "row" :wrap "wrap" :gap "2"}
    (for [tag (take 15 all-tags)]
      ^{:key tag}
      [:> Badge {:variant (if (contains? selected-tags tag) "solid" "outline")
                 :color (if (contains? selected-tags tag) "" "gray")
                 :highContrast (if (contains? selected-tags tag) false true)
                 :size "2"
                 :class "cursor-pointer hover:opacity-80 transition-opacity"
                 :on-click #(on-tag-change tag)}
       tag])]])

(defn modal-overview-tab [_connection overview _features]
  [:div {:class "space-y-6"}
   (when (:description overview)
     [:div
      [:> Text {:class "text-gray-700 leading-relaxed"}
       (:description overview)]])

   [:div
    [:> Text {:size "3" :weight "bold" :class "block mb-3 text-gray-900"}
     "Security Features"]
    [:ul {:class "list-disc list-inside space-y-2 text-gray-700"}
     [:li [:> Text {:size "2"} "Zero-Copy Data Loss Prevention"]]
     [:li [:> Text {:size "2"} "AI-powered data masking automatically redacts sensitive fields in real-time"]]
     [:li [:> Text {:size "2"} "No more rules or manual configuration required"]]
     [:li [:> Text {:size "2"} "Support for data masking at the protocol layer"]]]]

   [:div
    [:> Text {:size "3" :weight "bold" :class "block mb-3 text-gray-900"}
     "Complete Audit Trail"]
    [:ul {:class "list-disc list-inside space-y-2 text-gray-700"}
     [:li [:> Text {:size "2"} "Every query is logged with full context"]]
     [:li [:> Text {:size "2"} "Session recordings capture all database interactions"]]
     [:li [:> Text {:size "2"} "Searchable audit logs for compliance and debugging"]]
     [:li [:> Text {:size "2"} "Request-time-based access to production databases"]]
     [:li [:> Text {:size "2"} "Multi-party approval workflows"]]
     [:li [:> Text {:size "2"} "Automatic access revocation after specified duration"]]]]

   (when (:useCases overview)
     [:div
      [:> Text {:size "3" :weight "bold" :class "block mb-3 text-gray-900"}
       "Use Cases"]
      [:ul {:class "list-disc list-inside space-y-2 text-gray-700"}
       (for [use-case (:useCases overview)]
         ^{:key use-case}
         [:li [:> Text {:size "2"} use-case]])]])])

(defn modal-setup-tab [setupGuide]
  [:div {:class "space-y-6"}
   [:div
    [:> Text {:size "3" :weight "bold" :class "block mb-4 text-gray-900"}
     "Setup Requirements"]
    [:div {:class "space-y-3"}
     (let [requirements (or (:requirements setupGuide)
                            ["Network connectivity to service"
                             "Valid authentication credentials"
                             "Appropriate access permissions"])]
       (for [req requirements]
         ^{:key req}
         [:div {:class "flex items-start gap-3"}
          [:div {:class "w-2 h-2 bg-blue-500 rounded-full mt-2 flex-shrink-0"}]
          [:> Text {:size "2" :class "text-gray-700"} req]]))]]

   (when-let [access-methods (get-in setupGuide [:accessMethods])]
     [:div
      [:> Text {:size "3" :weight "bold" :class "block mb-4 text-gray-900"}
       "Access Methods"]
      [:div {:class "flex gap-3"}
       (when (:webapp access-methods)
         [:> Badge {:color "green" :size "2"} "Web App"])
       (when (:cli access-methods)
         [:> Badge {:color "blue" :size "2"} "CLI"])
       (when (:runbooks access-methods)
         [:> Badge {:color "purple" :size "2"} "Runbooks"])]])])

(defn modal-advanced-tab [connection]
  [:div {:class "space-y-6"}
   [:div
    [:> Text {:size "3" :weight "bold" :class "block mb-3 text-gray-900"}
     "Connection String Format"]
    [:> Text {:size "2" :class "font-mono bg-gray-100 px-3 py-2 rounded text-gray-800 block"}
     (or (get-in connection [:advancedConfiguration :connectionString])
         "connection://user:password@host:port/database")]]

   [:div
    [:> Text {:size "3" :weight "bold" :class "block mb-3 text-gray-900"}
     "Feature Configuration"]
    [:ul {:class "list-disc list-inside space-y-2 text-gray-700"}
     (let [configs (or (get-in connection [:advancedConfiguration :featureConfiguration])
                       ["SSL/TLS encryption setup"
                        "Connection pooling configuration"
                        "Performance optimization settings"
                        "Authentication and authorization"])]
       (for [config configs]
         ^{:key config}
         [:li [:> Text {:size "2"} config]]))]]])

(defn connection-detail-modal [connection open? on-close]
  (when connection
    (let [{:keys [name description overview features setupGuide]} connection
          badge (get-connection-badge (:id connection))]

      [:> Dialog.Root {:open open?}
       [:> Dialog.Content {:size "4"
                           :max-width "1000px"
                           :class "max-h-[85vh] overflow-hidden"}
        [:> Flex {:align "center" :justify "between" :gap "3"}
         [:> Box {:class "w-[60%]"}
          [:> Dialog.Title
           [:> Flex {:align "center" :items "center" :gap "2"}
            [:> Text {:size "8" :weight "bold" :class "text-gray-12"}
             name]
            (when badge
              [:> Badge {:color (:color badge) :size "1"}
               (:text badge)])]]

          [:> Dialog.Description {:class "mb-6"}
           [:> Text {:color "gray" :size "3"} description]]]

         [:> Flex {:gap "3" :class "mb-6"}
          [:> Button {:variant "soft" :size "3"}
           "View Docs"]
          [:> Button {:variant "solid" :size "3"}
           (str "Continue with " name)]]]

        ;; Tabs with Radix UI
        [:> Tabs.Root {:default-value "overview" :class "w-full"}
         [:> Tabs.List {:class "border-b border-gray-200 mb-6"}
          [:> Tabs.Trigger {:value "overview" :class "pb-3 text-sm font-medium"}
           "Overview"]
          [:> Tabs.Trigger {:value "setup-guide" :class "pb-3 text-sm font-medium"}
           "Setup Guide"]
          [:> Tabs.Trigger {:value "advanced" :class "pb-3 text-sm font-medium"}
           "Advanced Configuration"]]

         [:> Tabs.Content {:value "overview" :class "outline-none"}
          [:> ScrollArea {:class "max-h-[400px] overflow-auto pr-4"}
           [modal-overview-tab connection overview features]]]

         [:> Tabs.Content {:value "setup-guide" :class "outline-none"}
          [:> ScrollArea {:class "max-h-[400px] overflow-auto pr-4"}
           [modal-setup-tab setupGuide]]]

         [:> Tabs.Content {:value "advanced" :class "outline-none"}
          [:> ScrollArea {:class "max-h-[400px] overflow-auto pr-4"}
           [modal-advanced-tab connection]]]]]])))

(defn main-panel []
  (let [connections-metadata (rf/subscribe [:connections->metadata])
        search-term (r/atom "")
        selected-categories (r/atom #{})
        selected-tags (r/atom #{})
        selected-connection (r/atom nil)
        modal-open? (r/atom false)]

    ;; Load metadata if not loaded
    (when (nil? @connections-metadata)
      (rf/dispatch [:connections->load-metadata]))

    (fn []
      (if-not @connections-metadata
        [:> Box {:class "flex items-center justify-center h-screen bg-gray-50"}
         [:> Text {:size "4"} "Loading resource catalog..."]]

        (let [connections (:connections @connections-metadata)
              all-categories (->> connections
                                  (map :category)
                                  (remove nil?)
                                  distinct
                                  sort)
              all-tags (->> connections
                            (mapcat :tags)
                            (remove nil?)
                            distinct
                            (take 20)
                            sort)

              ;; Filter connections
              filtered-connections (->> connections
                                        (filter (fn [conn]
                                                  (and
                                                   ;; Search filter
                                                   (if (cs/blank? @search-term)
                                                     true
                                                     (or (cs/includes? (cs/lower-case (:name conn))
                                                                       (cs/lower-case @search-term))
                                                         (cs/includes? (cs/lower-case (or (:description conn) ""))
                                                                       (cs/lower-case @search-term))
                                                         (some #(cs/includes? (cs/lower-case %)
                                                                              (cs/lower-case @search-term))
                                                               (:tags conn))))
                                                   ;; Category filter
                                                   (if (empty? @selected-categories)
                                                     true
                                                     (contains? @selected-categories (:category conn)))
                                                   ;; Tags filter
                                                   (if (empty? @selected-tags)
                                                     true
                                                     (some @selected-tags (:tags conn)))))))

              popular-connections (->> filtered-connections
                                       (filter #(mock-popular-connections (:id %)))
                                       (take 5))

              connections-by-category (->> filtered-connections
                                           (group-by :category)
                                           (into (sorted-map)))]

          [:> Box {:class "h-screen bg-gray-50 flex overflow-hidden"}
           ;; Sidebar
           [:> Box {:class "w-80 flex flex-col"}
            [:> Box {:class "p-6 space-y-radix-8 flex-1 overflow-y-auto"}
             [search-section @search-term #(reset! search-term %)]
             [categories-filter @selected-categories
              (fn [category]
                (if (contains? @selected-categories category)
                  (swap! selected-categories disj category)
                  (swap! selected-categories conj category)))
              all-categories]
             [tags-filter @selected-tags
              (fn [tag]
                (if (contains? @selected-tags tag)
                  (swap! selected-tags disj tag)
                  (swap! selected-tags conj tag)))
              all-tags]]]

           ;; Main content
           [:> Box {:class "flex-1 flex flex-col overflow-hidden"}
            [:> Box {:class "p-8 flex-1 overflow-y-auto"}
             [:> Box {:class "max-w-7xl space-y-radix-9 mx-auto"}
              [:> Box {:class "mb-12"}
               [:> Heading {:as "h2" :size "6" :weight "bold" :class "mb-3 text-[--gray-12]"}
                "Getting Started"]
               [:> Text {:as "p" :size "3" :class "text-[--gray-12]"}
                "Setup your environment by selecting your Resource type:"]]

              ;; Popular section
              (when (seq popular-connections)
                [category-section "Popular" popular-connections
                 (fn [connection]
                   (reset! selected-connection connection)
                   (reset! modal-open? true))])

              ;; Category sections
              (for [[category connections] connections-by-category]
                ^{:key category}
                [category-section (cs/replace (cs/capitalize category) #"-" " ")
                 connections
                 (fn [connection]
                   (reset! selected-connection connection)
                   (reset! modal-open? true))])]]]

           ;; Modal
           [connection-detail-modal @selected-connection @modal-open?
            #(reset! modal-open? false)]])))))

;; Export for use in routing
(defn panel []
  [main-panel])
