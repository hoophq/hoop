(ns webapp.components.command-palette-pages
  (:require
   ["cmdk" :as cmdk]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.components.command-palette-constants :as constants]))

(def CommandGroup (.-CommandGroup cmdk))
(def CommandItem (.-CommandItem cmdk))
(def CommandSeparator (.-CommandSeparator cmdk))

(defn action-item
  "Componente genérico para item de ação"
  [{:keys [id label icon] :as item}]
  [:> CommandItem
   {:key id
    :value label
    :keywords [label]
    :onSelect (fn []
                (rf/dispatch [:command-palette->execute-action item]))}
   [:div {:class "flex items-center gap-2"}
    (if (fn? icon)
      [icon]
      [:> icon {:size 16 :class "text-gray-11"}])
    [:div {:class "flex flex-col"}
     [:span {:class "text-sm font-medium"} label]]]])

(defn connection-result-item
  "Item de resultado de busca de conexão"
  [connection]
  [:> CommandItem
   {:key (:id connection)
    :value (:name connection)
    :keywords [(:type connection) (:subtype connection) (:status connection) "connection"]
    :onSelect (fn []
                (rf/dispatch [:command-palette->navigate-to-page :connection-actions connection]))}
   [:div {:class "flex items-center gap-2"}
    [:div {:class "flex flex-col"}
     [:span {:class "text-sm font-medium"} (:name connection)]]]])

(defn runbook-result-item
  "Item de resultado de busca de runbook"
  [runbook-path]
  (let [filename (last (cs/split runbook-path #"/"))]
    [:> CommandItem
     {:key runbook-path
      :value filename
      :keywords ["runbook" "script" "sql"]
      :onSelect (fn []
                  (rf/dispatch [:command-palette->close])
                  (js/console.log "Navigate to runbook:" runbook-path))}
     [:div {:class "flex items-center gap-2"}
      [:div {:class "flex flex-col"}
       [:span {:class "text-sm font-medium"} filename]
       [:span {:class "text-xs text-gray-11"} runbook-path]]]]))

(defn main-page
  "Página principal com todas as páginas + busca"
  [search-results]
  (let [search-status (:status search-results)
        connections (:connections (:data search-results))
        runbooks (:runbooks (:data search-results))]
    [:<>
     ;; Resultados de busca (se houver)
     (when (and (= search-status :ready) (or (seq connections) (seq runbooks)))
       [:<>
        (when (seq connections)
          [:> CommandGroup
           {:heading "Connections"}
           (for [connection connections]
             ^{:key (:id connection)}
             [connection-result-item connection])])

        (when (seq runbooks)
          [:> CommandGroup
           {:heading "Runbooks"}
           (for [runbook runbooks]
             ^{:key runbook}
             [runbook-result-item runbook])])

        [:> CommandSeparator]])

     ;; Páginas estáticas (sempre visíveis)
     [:> CommandGroup
      {:heading "Quick Access"}
      (for [item constants/main-navigation-items]
        ^{:key (:id item)}
        [action-item item])]]))

(defn connection-actions-page
  "Página de ações para uma conexão específica"
  [connection]
  (let [connection-type (keyword (:type connection))
        actions (get constants/connection-actions connection-type
                     (:default constants/connection-actions))]
    [:> CommandGroup
     (for [action actions]
       ^{:key (:id action)}
       [action-item (assoc action
                           :connection-name (:name connection)
                           :connection-id (:id connection))])]))
