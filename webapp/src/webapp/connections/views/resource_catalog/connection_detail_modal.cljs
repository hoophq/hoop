(ns webapp.connections.views.resource-catalog.connection-detail-modal
  (:require
   ["@radix-ui/themes" :refer [Avatar Badge Box Button Card Dialog Flex
                               Heading Link ScrollArea Tabs Text]]
   ["lucide-react" :refer [BookMarked Monitor SquareTerminal]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.text-with-markdown-link :as text-with-markdown-link]))

(def mock-new-connections #{"postgres-demo"})
(def mock-beta-connections #{"mongodb" "aws-discovery"})

(defn get-connection-badge [connection]
  (let [connection-id (if (map? connection) (:id connection) connection)]
    (cond
      ;; Mock badges
      (mock-new-connections connection-id) {:text "NEW" :color "green"}
      (mock-beta-connections connection-id) {:text "BETA" :color "indigo"}
      :else nil)))

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

(defn modal-overview-tab [overview setupGuide]
  [:> Box {:class "space-y-6"}
   (when (:description overview)
     [:> Box
      [text-with-markdown-link/main
       (:description overview)
       {:size "3" :class "text-gray-12"}
       {:size "3" :target "_blank" :class "text-blue-12"}]])

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
       (for [credential-info credentials]
         ^{:key (:name credential-info)}
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
               "filesystem" "File Content"
               "textarea" "Text Content"
               (:type credential-info))]]
           [text-with-markdown-link/main
            (:description credential-info)
            {:size "2" :class "text-gray-11"}
            {:size "2" :target "_blank" :class "text-blue-11"}]]])]])])

(defn main [connection open? on-close]
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
          (when (get-in connection [:resourceConfiguration :credentials])
            [:> Tabs.Trigger {:value "setup-guide" :class "pb-3 text-sm font-medium"}
             "Setup Guide"])]

         [:> Tabs.Content {:value "overview" :class "outline-none"}
          [:> ScrollArea {:class "max-h-[400px] overflow-auto pr-4"}
           [modal-overview-tab overview setupGuide]]]

         [:> Tabs.Content {:value "setup-guide" :class "outline-none"}
          [:> ScrollArea {:class "max-h-[400px] overflow-auto pr-4"}
           [modal-setup-tab connection]]]]]])))
