(ns webapp.components.command-palette
  (:require
   ["cmdk" :as cmdk]
   ["lucide-react" :refer [Search Hash Database Terminal FileText]]
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
(def CommandLoading (.-CommandLoading cmdk))
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
  [{:keys [id name type subtype status]}]
  [:> CommandItem
   {:key id
    :value (str name " " type " " subtype)
    :onSelect (fn []
                (rf/dispatch [:command-palette->close])
                (rf/dispatch [:navigate :connection-detail {:id id}]))}
   [:div {:class "flex items-center gap-3 py-2"}
    [:> (connection-icon type) {:size 16 :class "text-gray-11"}]
    [:div {:class "flex flex-col"}
     [:span {:class "text-sm font-medium"} name]
     [:div {:class "flex items-center gap-2 text-xs text-gray-11"}
      [:span subtype]
      (when (= status "online")
        [:div {:class "w-2 h-2 bg-green-9 rounded-full"}])]]]])

(defn runbook-item
  "Componente para item de runbook"
  [runbook-path]
  (let [filename (last (cs/split runbook-path #"/"))]
    [:> CommandItem
     {:key runbook-path
      :value runbook-path
      :onSelect (fn []
                  (rf/dispatch [:command-palette->close])
                  ;; TODO: Implementar navegação para runbook
                  (js/console.log "Navigate to runbook:" runbook-path))}
     [:div {:class "flex items-center gap-3 py-2"}
      [:> FileText {:size 16 :class "text-gray-11"}]
      [:div {:class "flex flex-col"}
       [:span {:class "text-sm font-medium"} filename]
       [:span {:class "text-xs text-gray-11"} runbook-path]]]]))

(defn command-palette
  "Componente principal do command palette"
  []
  (let [palette-state @(rf/subscribe [:command-palette])
        search-results @(rf/subscribe [:command-palette->search-results])
        status (:status search-results)
        ;; Mostrar indicador sutil de busca apenas no ícone
        is-searching? (or (= status :searching) (= status :loading))]
    [:> CommandDialog
     {:open (:open? palette-state)
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
       [:> CommandInput
        {:placeholder "Buscar conexões e runbooks..."
         :className "flex-1 bg-transparent border-none outline-none text-sm placeholder:text-gray-11"
         :onValueChange #(rf/dispatch [:command-palette->search %])}]]

      [:> CommandList
       {:className "flex-1 overflow-y-auto p-2"}

       [:> CommandEmpty
        {:className "flex items-center justify-center text-center text-sm text-gray-11 h-full"}
        (if (= status :idle)
          "Digite pelo menos 2 caracteres para buscar."
          "Nenhum resultado encontrado.")]

       (let [connections (:connections (:data search-results))
             runbooks (:runbooks (:data search-results))]
         [:<>
          (when (seq connections)
            [:> CommandGroup
             {:heading "Conexões"}
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
               [runbook-item runbook])])])]]]))

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
