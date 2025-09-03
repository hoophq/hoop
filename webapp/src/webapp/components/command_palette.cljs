(ns webapp.components.command-palette
  (:require
   ["cmdk" :as cmdk]
   ["@radix-ui/themes" :refer [Text]]
   ["lucide-react" :refer [Search X]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.command-palette-pages :as pages]))

(def Command (.-Command cmdk))
(def CommandDialog (.-CommandDialog cmdk))
(def CommandInput (.-CommandInput cmdk))
(def CommandList (.-CommandList cmdk))
(def CommandEmpty (.-CommandEmpty cmdk))

(defn breadcrumb-tag
  "Tag mostrando o contexto atual"
  [current-page context]
  (let [label (case current-page
                :connections "Connections"
                :features "Features"
                :organization "Organization"
                :connection-actions (:name context)
                :connections-search "Connections"
                :runbooks-search "Runbooks"
                (str current-page))]
    [:div {:class "flex items-center gap-2 bg-gray-3 px-2 py-1 rounded-full"}
     [:> Text
      {:size "1"
       :weight "medium"
       :class "text-[--gray-11]"}
      label]
     [:button {:class "hover:bg-gray-5 rounded p-0.5 transition-colors"
               :on-click #(rf/dispatch [:command-palette->back])}
      [:> X {:size 12}]]]))

(defn enhanced-empty-state
  "Empty state melhorado"
  [current-status current-page]
  [:> CommandEmpty
   {:className "flex items-center justify-center text-center text-sm text-gray-11 h-full"}
   (case [current-status current-page]
     [:idle :main] "Search for resources, features and more..."
     [:idle :connection-actions] "Choose an action for this connection"
     "No results found.")])



(defn command-palette
  "Componente principal do command palette"
  []
  (let [palette-state (rf/subscribe [:command-palette])
        search-results (rf/subscribe [:command-palette->search-results])
        db-user (rf/subscribe [:users->current-user])]
    (fn []
      (let [status (:status @search-results)
            current-page (:current-page @palette-state)
            context (:context @palette-state)
            user-data (:data @db-user)
            ;; Mostrar indicador sutil de busca apenas no ícone
            is-searching? (or (= status :searching) (= status :loading))
            ;; Placeholder dinâmico baseado na página atual
            placeholder (case current-page
                          :main "Search for resources, features and more..."
                          :connection-actions "Select or search an action"
                          "Search...")]
        [:> Command
         {:shouldFilter false  ; Usar filtro manual para busca assíncrona
          :onKeyDown (fn [e]
                       ;; Navegação por teclado
                       (when (or (= (.-key e) "Escape")
                                 (and (= (.-key e) "Backspace")
                                      (empty? (or (:query @palette-state) ""))))
                         (when (not= current-page :main)
                           (.preventDefault e)
                           (rf/dispatch [:command-palette->back]))))}

         [:> CommandDialog
          {:open (:open? @palette-state)
           :label "Command Palette"
           :container (js/document.querySelector ".radix-themes")
           :onOpenChange #(if %
                            (rf/dispatch [:command-palette->open])
                            (rf/dispatch [:command-palette->close]))
           :className "fixed inset-0 z-50 flex items-start justify-center pt-[20vh]"}

          ;; Overlay manual para clique fora com blur
          [:div {:class "fixed inset-0 bg-black/10 backdrop-blur-sm"
                 :on-click #(rf/dispatch [:command-palette->close])}]

          [:div {:class "w-full max-w-2xl bg-white rounded-lg shadow-2xl border border-gray-6 overflow-hidden h-96 flex flex-col relative z-10"}
           [:div {:class "flex items-center gap-3 px-4 py-3 border-b border-gray-6"}
            [:> Search {:size 16
                        :class (str "transition-colors duration-200 "
                                    (if is-searching?
                                      "text-blue-9"
                                      "text-gray-11"))}]
            [:div {:class "flex items-center gap-2 flex-1"}
             [:> CommandInput
              {:placeholder placeholder
               :value (or (:query @palette-state) "")
               :className "flex-1 bg-transparent border-none outline-none text-sm placeholder:text-gray-11"
               :onValueChange #(rf/dispatch [:command-palette->search (or % "")])}]

             ;; Breadcrumb quando não estiver na página principal
             (when (not= current-page :main)
               [breadcrumb-tag current-page context])]]

           [:> CommandList
            {:className "flex-1 overflow-y-auto p-4"}

            [enhanced-empty-state status current-page]

            ;; Renderizar conteúdo baseado na página atual
            (println user-data)
            (case current-page
              :main
              [pages/main-page @search-results user-data]

              :connection-actions
              [pages/connection-actions-page context]

              ;; Default: página principal
              [pages/main-page @search-results user-data])]]]]))))

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
