(ns webapp.components.command-palette
  (:require
   ["cmdk" :as cmdk]
   ["@radix-ui/themes" :refer [Text]]
   ["lucide-react" :refer [Search Hash Database Terminal FileText Globe Monitor Settings HelpCircle X]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]))

(def Command (.-Command cmdk))
(def CommandDialog (.-CommandDialog cmdk))
(def CommandInput (.-CommandInput cmdk))
(def CommandList (.-CommandList cmdk))
(def CommandEmpty (.-CommandEmpty cmdk))
(def CommandGroup (.-CommandGroup cmdk))
(def CommandItem (.-CommandItem cmdk))
(def CommandSeparator (.-CommandSeparator cmdk))

(defn connection-icon
  "Retorna o ícone apropriado baseado no tipo de conexão"
  [connection-type]
  (case connection-type
    "database" Database
    "terminal" Terminal
    Hash))

(defn connection-item
  "Componente para item de conexão"
  [{:keys [id name type subtype status] :as connection}]
  [:> CommandItem
   {:key id
    :value name
    :keywords [type subtype status "connection" "database" "terminal"]
    :onSelect (fn []
                (rf/dispatch [:command-palette->show-connection-actions connection]))}
   [:div {:class "flex items-center gap-2"}
    [:> (connection-icon type) {:size 16 :class "text-gray-11"}]
    [:div {:class "flex flex-col"}
     [:> Text {:size "2"} name]]]])

(defn runbook-item
  "Componente para item de runbook"
  [runbook-path]
  (let [filename (last (cs/split runbook-path #"/"))
        directory (cs/join "/" (butlast (cs/split runbook-path #"/")))]
    [:> CommandItem
     {:key runbook-path
      :value filename
      :keywords ["runbook" "script" "sql" directory (cs/replace filename #"\.(sql|sh|py|js)$" "")]
      :onSelect (fn []
                  (rf/dispatch [:command-palette->close])
                  ;; TODO: Implementar navegação para runbook
                  (js/console.log "Navigate to runbook:" runbook-path))}
     [:div {:class "flex items-center gap-2"}
      [:> FileText {:size 16 :class "text-gray-11"}]
      [:div {:class "flex flex-col"}
       [:span {:class "text-sm font-medium"} filename]
       [:span {:class "text-xs"} runbook-path]]]]))

(defn connection-action-item
  "Componente para ação de uma conexão"
  [{:keys [id icon label action]}]
  [:> CommandItem
   {:key id
    :value label
    :keywords [label]
    :onSelect (fn []
                (rf/dispatch [:command-palette->close])
                (when action (action)))}
   [:div {:class "flex items-center gap-2"}
    [:> icon {:size 16 :class "text-gray-11"}]
    [:div {:class "flex flex-col"}
     [:> Text {:class "hover:text-[--gray-12]" :size "2"} label]]]])

(defn connection-actions-list
  "Lista de ações disponíveis para uma conexão"
  [{:keys [name type]}]
  (let [actions (case type
                  "database"
                  [{:id "web-terminal"
                    :icon Globe
                    :label "Open in Web Terminal"
                    :action #(rf/dispatch [:navigate :web-terminal {:connection-name name}])}
                   {:id "local-terminal"
                    :icon Terminal
                    :label "Open in Local Terminal"
                    :action #(js/console.log "Open local terminal for" name)}
                   {:id "native-client"
                    :icon Monitor
                    :label "Open in Native Client"
                    :action #(js/console.log "Open native client for" name)}
                   {:id "runbooks"
                    :icon FileText
                    :label "Open in Runbooks"
                    :action #(rf/dispatch [:navigate :runbooks {:connection-name name}])}
                   {:id "configure"
                    :icon Settings
                    :label "Configure Connection"
                    :action #(rf/dispatch [:navigate :edit-connection {:connection-name name}])}
                   {:id "help"
                    :icon HelpCircle
                    :label "How to connect via CLI?"
                    :description "Docs"
                    :action #(js/window.open "https://docs.hoop.dev" "_blank")}]

                  "custom"
                  [{:id "web-terminal"
                    :icon Globe
                    :label "Open in Web Terminal"
                    :action #(rf/dispatch [:navigate :web-terminal {:connection-name name}])}
                   {:id "configure"
                    :icon Settings
                    :label "Configure Connection"
                    :action #(rf/dispatch [:navigate :edit-connection {:connection-name name}])}]

                  ;; Default actions
                  [{:id "configure"
                    :icon Settings
                    :label "Configure Connection"
                    :action #(rf/dispatch [:navigate :edit-connection {:connection-name name}])}])]
    [:<>
     (for [action actions]
       ^{:key (:id action)}
       [connection-action-item action])]))

(defn connection-tag
  "Tag mostrando a conexão selecionada"
  [connection]
  [:div {:class "flex items-center gap-2 bg-[--gray-a3] px-2 py-1 rounded-full"}
   [:> Text {:size "1" :weight "medium" :class "text-[--gray-11]"}
    (:name connection)]
   [:button {:class "hover:bg-gray-5 rounded p-0.5 transition-colors"
             :on-click #(rf/dispatch [:command-palette->back-to-main])}
    [:> X {:size 12}]]])

(defn enhanced-empty-state
  "Empty state melhorado"
  [current-status]
  [:> CommandEmpty
   {:className "flex items-center justify-center text-center text-sm text-gray-11 h-full"}
   (cond
     (= current-status :idle) "Digite pelo menos 2 caracteres para buscar."
     :else "Nenhum resultado encontrado.")])

(defn command-palette
  "Componente principal do command palette"
  []
  (let [palette-state (rf/subscribe [:command-palette])
        search-results (rf/subscribe [:command-palette->search-results])]
    (fn []
      (let [status (:status @search-results)
            current-page (:current-page @palette-state)
            selected-connection (:selected-connection @palette-state)
            ;; Mostrar indicador sutil de busca apenas no ícone
            is-searching? (or (= status :searching) (= status :loading))
            ;; Placeholder dinâmico baseado na página atual
            placeholder (case current-page
                          :connection-actions "Select or search an action"
                          "Buscar conexões e runbooks...")]
        [:> Command
         {:shouldFilter false  ; Usar filtro manual para busca assíncrona
          :onKeyDown (fn [e]
                       ;; Navegação por teclado
                       (when (or (= (.-key e) "Escape")
                                 (and (= (.-key e) "Backspace")
                                      (empty? (or (:query @palette-state) ""))))
                         (when (= current-page :connection-actions)
                           (.preventDefault e)
                           (rf/dispatch [:command-palette->back-to-main]))))}

         [:> CommandDialog
          {:open (:open? @palette-state)
           :label "Command Palette"
           :container (.querySelector js/document ".radix-themes")
           :onOpenChange #(if %
                            (rf/dispatch [:command-palette->open])
                            (rf/dispatch [:command-palette->close]))
           :className "fixed inset-0 z-50 flex items-start justify-center pt-[20vh]"}

          [:div {:class "w-full max-w-2xl bg-white rounded-lg shadow-2xl border border-gray-6 overflow-hidden h-96 flex flex-col"}
           [:div {:class "flex items-center gap-3 px-4 py-3 border-b border-gray-6"}
            [:> Search {:size 16
                        :class (str "transition-colors duration-200 "
                                    (if is-searching?
                                      "text-blue-9"
                                      "text-gray-11"))}]
            [:div {:class "flex items-center gap-2 flex-1"}
             ;; Tag da conexão selecionada (quando em connection-actions)
             [:> CommandInput
              {:placeholder placeholder
               :value (or (:query @palette-state) "")
               :className "flex-1 bg-transparent border-none outline-none text-sm placeholder:text-gray-11"
               :onValueChange #(rf/dispatch [:command-palette->search (or % "")])}]

             (when (and (= current-page :connection-actions) selected-connection)
               (println "connection-tag" selected-connection)
               [connection-tag selected-connection])]]

           [:> CommandList
            {:className "flex-1 overflow-y-auto p-4"}

            [enhanced-empty-state status]

            ;; Renderização condicional baseada na página atual
            (case current-page
              :connection-actions
              ;; Página de ações da conexão
              (when selected-connection
                [connection-actions-list selected-connection])

              ;; Página principal (default)
              (let [connections (:connections (:data @search-results))
                    runbooks (:runbooks (:data @search-results))]
                [:<>
                 (when (seq connections)
                   [:> CommandGroup
                    (for [connection connections]
                      ^{:key (:id connection)}
                      [connection-item connection])])

                 (when (and (seq connections) (seq runbooks))
                   [:> CommandSeparator])

                 (when (seq runbooks)
                   [:> CommandGroup
                    {:heading "Runbooks"}
                    (for [runbook runbooks]
                      ^{:key runbook}
                      [runbook-item runbook])])]))]]]]))))

(defn keyboard-listener
  "Componente para capturar CMD+K / Ctrl+K"
  []
  (r/create-class
   {:component-did-mount
    (fn []
      (let [handle-keydown
            (fn [e]
              (when (and (or (.-metaKey e) (.-ctrlKey e))
                         (= (.-key e) "k"))
                (.preventDefault e)
                (rf/dispatch [:command-palette->toggle])))]
        (js/document.addEventListener "keydown" handle-keydown)
        ;; Armazenar a função para cleanup
        (set! (.-keydownHandler js/document) handle-keydown)))

    :component-will-unmount
    (fn []
      (when (.-keydownHandler js/document)
        (js/document.removeEventListener "keydown" (.-keydownHandler js/document))
        (set! (.-keydownHandler js/document) nil)))

    :reagent-render
    (fn [] nil)}))
