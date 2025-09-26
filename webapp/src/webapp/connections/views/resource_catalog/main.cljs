(ns webapp.connections.views.resource-catalog.main
  (:require
   ["@radix-ui/themes" :refer [Box Heading Text]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.connections.views.resource-catalog.category-section :as category-section]
   [webapp.connections.views.resource-catalog.connection-detail-modal :as connection-detail-modal]
   [webapp.connections.views.resource-catalog.filters :as filters]))

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

;; Mock data
(def mock-popular-connections #{"postgres" "mysql" "mongodb" "ssh" "linux-vm"
                                "postgres-demo"})

(defn main []
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
             [filters/search-section @search-term #(reset! search-term %)]
             [filters/categories-filter @selected-categories
              (fn [category]
                (if (contains? @selected-categories category)
                  (swap! selected-categories disj category)
                  (swap! selected-categories conj category)))
              all-categories]
             [filters/tags-filter @selected-tags
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
                [category-section/main "Popular" popular-connections
                 (fn [connection]
                   (reset! selected-connection connection)
                   (reset! modal-open? true))])

              ;; Category sections
              (for [[category connections] connections-by-category]
                ^{:key category}
                [category-section/main (cs/replace (cs/capitalize category) #"-" " ")
                 connections
                 (fn [connection]
                   (reset! selected-connection connection)
                   (reset! modal-open? true))])]]]

           ;; Modal
           [connection-detail-modal/main @selected-connection @modal-open?
            #(reset! modal-open? false)]])))))
