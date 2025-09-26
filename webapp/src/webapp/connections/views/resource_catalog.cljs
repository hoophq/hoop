(ns webapp.connections.views.resource-catalog
  (:require
   ["@radix-ui/themes" :refer [Avatar Badge Box Button Card Dialog Flex Link
                               Heading ScrollArea Tabs Text]]
   ["lucide-react" :refer [Check Monitor BookMarked SquareTerminal]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

;; Denylist - conexões que NÃO aparecerão no catálogo
(def denied-connections #{})

;; Conexões custom que não estão no metadata.json
(def custom-connections
  [;; Infrastructure
   {:id "linux-vm"
    :name "Linux VM or Container"
    :description "Connect to Linux virtual machines, Docker containers, or any remote server via SSH."
    :category "infrastructure-access"
    :icon-name "ssh"
    :tags ["linux" "vm" "container" "ssh" "infrastructure"]
    :overview {:description "Connect to any Linux-based system including virtual machines, Docker containers, bare metal servers, and cloud instances."
               :features ["Secure SSH-based access"
                          "Terminal session recording"
                          "Multi-user access control"
                          "Session sharing and collaboration"]
               :useCases ["Development environment access"
                          "Production server administration"
                          "Container debugging and management"
                          "Infrastructure maintenance and monitoring"]}
    :setupGuide {:accessMethods {:webapp true :cli true :runbooks true}
                 :requirements ["SSH server running on target system"
                                "Valid SSH credentials (password or key-based)"
                                "Network connectivity to port 22"
                                "Proper firewall configuration"]}
    :resourceConfiguration {:type "server"
                            :subtype "custom"
                            :credentials {}}}])

;; Conexões especiais apenas para onboarding (executam ações diretas)
(def onboarding-special-connections
  [{:id "postgres-demo"
    :name "Demo PostgresSQL"
    :description "Access a preloaded database to see it in action."
    :category "quickstart"
    :icon-name "postgres"
    :tags ["demo" "quickstart" "postgresql"]
    :action #(rf/dispatch [:connections->quickstart-create-postgres-demo])
    :special-type :action}
   {:id "aws-discovery"
    :name "Automatic resource discovery"
    :description "Access your resources through your infrastructure providers."
    :category "quickstart"
    :icon-name "aws"
    :tags ["aws" "discovery" "automatic" "beta"]
    :action #(rf/dispatch [:navigate :onboarding-resource-providers])
    :special-type :action}])

;; Mapeamento connection-id → setup flow
(defn connection-setup-mapping [connection]
  (let [connection-id (:id connection)
        resource-configuration (:resourceConfiguration connection)
        connection-mapped {;; Database hardcoded (fluxo atual)
                           "postgres" {:type "database" :subtype "postgres"}
                           "mysql" {:type "database" :subtype "mysql"}
                           "mongodb" {:type "database" :subtype "mongodb"}
                           "mssql" {:type "database" :subtype "mssql"}
                           "oracle" {:type "database" :subtype "oracledb"}
                           ;; Network hardcoded (fluxo atual)
                           "ssh" {:type "server" :subtype "ssh"}
                           "tcp" {:type "network" :subtype "tcp"}
                           "httpproxy" {:type "network" :subtype "httpproxy"}
                           ;; Custom connections
                           "linux-vm" {:type "server" :subtype "custom"}}]

    (if (get connection-mapped connection-id)
      (get connection-mapped connection-id)

      {:type (:type resource-configuration)
       :subtype (:subtype resource-configuration)
       :command (:command resource-configuration)})))

;; Mock data
(def mock-popular-connections #{"postgres" "mysql" "mongodb" "ssh" "linux-vm"
                                "postgres-demo"})
(def mock-new-connections #{"postgres-demo"})
(def mock-beta-connections #{"mongodb" "aws-discovery"})

(defn get-connection-badge [connection]
  (let [connection-id (if (map? connection) (:id connection) connection)]
    (cond
      ;; Mock badges
      (mock-new-connections connection-id) {:text "NEW" :color "green"}
      (mock-beta-connections connection-id) {:text "BETA" :color "indigo"}
      :else nil)))

(defn navigate-to-setup
  "Navega para o setup flow com o tipo de conexão pré-selecionado"
  [connection]
  ;; Se é uma conexão especial com ação direta, executa a ação
  (if (= (:special-type connection) :action)
    ((:action connection))
    ;; Senão, segue o fluxo normal de setup
    (let [setup-config (connection-setup-mapping connection)]
      (if setup-config
        (do
          ;; Inicializa o setup com configurações do catálogo
          (rf/dispatch [:connection-setup/initialize-from-catalog setup-config])
          ;; Se tem app-type, seleciona também
          (when (:app-type setup-config)
            (rf/dispatch [:connection-setup/select-app-type (:app-type setup-config)]))
          ;; Detecta se estamos no contexto de onboarding pela URL atual
          (let [current-path (.. js/window -location -pathname)
                is-onboarding? (cs/includes? current-path "/onboarding")]
            ;; Navega para o lugar certo baseado no contexto
            (if is-onboarding?
              (rf/dispatch [:navigate :onboarding-setup-resource])
              (rf/dispatch [:navigate :create-connection]))))
        (js/console.warn "No setup mapping found for connection:" (:id connection))))))

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
        badge (get-connection-badge connection)]
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
                     :variant "solid"
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
        [connection-card connection (or (:action connection) on-connection-click)])]]))

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

(defn modal-overview-tab [overview setupGuide]
  [:div {:class "space-y-6"}
   (when (:description overview)
     [:div
      [:> Text {:class "text-gray-700 leading-relaxed"}
       (:description overview)]])

   (when-let [access-methods (get-in setupGuide [:accessMethods])]
     [:div
      [:> Text {:size "3" :weight "bold" :class "block mb-4 text-gray-900"}
       "Connection Methods"]
      [:div {:class "grid grid-cols-2 gap-4"}
       (when (:webapp access-methods)
         [:> Card {:size "1"}
          [:> Flex {:direction "column" :gap "3"}
           [:> Flex {:align "center" :gap "2"}
            [:> Avatar {:size "4"
                        :variant "soft"
                        :color "gray"
                        :fallback (r/as-element [:> Monitor {:size 18}])}]
            [:> Box
             [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-gray-12"} "Web App"]
             [:> Text {:as "p" :size "2" :class "text-gray-11"}
              "Access resources and execute commands directly from Web UI."]]]]])

       (when (:runbooks access-methods)
         [:> Card {:size "1"}
          [:> Flex {:direction "column" :gap "3"}
           [:> Flex {:align "center" :gap "2"}
            [:> Avatar {:size "4"
                        :variant "soft"
                        :color "gray"
                        :fallback (r/as-element [:> BookMarked {:size 18}])}]
            [:> Box
             [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-gray-12"} "Runbooks"]
             [:> Text {:as "p" :size "2" :class "text-gray-11"}
              "Execute securely git-based predefined scripts in your resources."]]]]])

       (when (:cli access-methods)
         [:> Card {:size "1"}
          [:> Flex {:direction "column" :gap "3"}
           [:> Flex {:align "center" :gap "2"}
            [:> Avatar {:size "4"
                        :variant "soft"
                        :color "gray"
                        :fallback (r/as-element [:> SquareTerminal {:size 18}])}]
            [:> Box
             [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-gray-12"} "Hoop CLI"]
             [:> Text {:as "p" :size "2" :class "text-gray-11"}
              "Access resources and execute commands natively in your favorite apps."]]]]])]])])

(defn modal-setup-tab [connection]
  [:div {:class "space-y-6"}

   ;; Required Configuration (moved from Advanced)
   (when-let [credentials (get-in connection [:resourceConfiguration :credentials])]
     [:div
      [:> Text {:size "3" :weight "bold" :class "block mb-4 text-gray-900"}
       "Configuration"]
      [:div {:class "space-y-3"}
       (for [[credential-key credential-info] credentials]
         ^{:key credential-key}
         [:> Card {:size "1"}
          [:> Flex {:direction "column" :gap "2"}
           [:> Flex {:align "center" :justify "between"}
            [:> Flex {:align "center" :gap "2"}
             [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-gray-12"}
              (:name credential-info)]
             (when (:required credential-info)
               [:> Badge {:size "1"} "Required"])]
            [:> Badge {:variant "soft" :color "gray" :size "1"}
             (case (:type credential-info)
               "env-var" "Environment Variable"
               "filesystem" "File Path"
               "textarea" "Text Content"
               (:type credential-info))]]
           [:> Text {:as "p" :size "2" :class "text-gray-11"}
            (:description credential-info)]]])]])])

(defn connection-detail-modal [connection open? on-close]
  (when connection
    (let [{:keys [name description overview setupGuide]} connection
          badge (get-connection-badge (:id connection))]

      [:> Dialog.Root {:open open?
                       :onOpenChange #(when-not % (on-close))}
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
          [:> Link {:href (str "https://hoop.dev/docs/"
                               (get-in connection [:documentationConfig :path]))
                    :target "_blank"}
           [:> Button {:variant "soft"
                       :size "3"}
            "View Docs"]]
          [:> Button {:variant "solid" :size "3"
                      :on-click #(navigate-to-setup connection)}
           "Continue Setup"]]]

        ;; Tabs with Radix UI
        [:> Tabs.Root {:default-value "overview" :class "w-full"}
         [:> Tabs.List {:class "border-b border-gray-200 mb-6"}
          [:> Tabs.Trigger {:value "overview" :class "pb-3 text-sm font-medium"}
           "Overview"]
          [:> Tabs.Trigger {:value "setup-guide" :class "pb-3 text-sm font-medium"}
           "Setup Guide"]]

         [:> Tabs.Content {:value "overview" :class "outline-none"}
          [:> ScrollArea {:class "max-h-[400px] overflow-auto pr-4"}
           [modal-overview-tab overview setupGuide]]]

         [:> Tabs.Content {:value "setup-guide" :class "outline-none"}
          [:> ScrollArea {:class "max-h-[400px] overflow-auto pr-4"}
           [modal-setup-tab connection]]]]]])))

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

        (let [metadata-connections (:connections @connections-metadata)
              ;; Aplicar denylist - remove conexões não desejadas
              filtered-metadata-connections (->> metadata-connections
                                                 (remove #(denied-connections (:id %))))
              ;; Detectar se estamos no onboarding
              current-path (.. js/window -location -pathname)
              is-onboarding? (cs/includes? current-path "/onboarding")
              ;; Combinar metadata + custom + specials (se onboarding)
              connections (concat filtered-metadata-connections
                                  custom-connections
                                  (when is-onboarding? onboarding-special-connections))
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
                                                     (some #(contains? @selected-tags %) (:tags conn)))))))

              ;; Popular connections - apenas quando não há filtros ativos
              has-any-filter? (or (not (cs/blank? @search-term))
                                  (not-empty @selected-categories)
                                  (not-empty @selected-tags))
              base-popular-connections (->> connections  ; Usa connections originais
                                            (filter #(mock-popular-connections (:id %)))
                                            (take 5))
              popular-connections (when-not has-any-filter?  ; Só mostra sem filtros
                                    (if is-onboarding?
                                      ;; No onboarding: specials primeiro, depois populares normais
                                      (concat onboarding-special-connections
                                              (take 3 base-popular-connections))
                                      ;; Fora do onboarding: apenas populares normais
                                      base-popular-connections))

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
              [:> Box {:class "space-y-radix-6 mb-12"}
               (when is-onboarding?
                 [:figure
                  [:img {:src "/images/hoop-branding/PNG/hoop-symbol_black@4x.png"
                         :alt "Hoop Logo"
                         :class "w-16"}]])
               [:> Box
                [:> Heading {:as "h2" :size "6" :weight "bold" :class "mb-3 text-[--gray-12]"}
                 "Getting Started"]
                [:> Text {:as "p" :size "3" :class "text-[--gray-12]"}
                 "Setup your environment by selecting your Resource type:"]]]

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
