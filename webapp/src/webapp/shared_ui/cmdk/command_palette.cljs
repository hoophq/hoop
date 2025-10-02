(ns webapp.shared-ui.cmdk.command-palette
  (:require
   ["cmdk" :refer [CommandEmpty]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.command-dialog :as command-dialog]
   [webapp.shared-ui.cmdk.command-palette-pages :as pages]))


(defn enhanced-empty-state
  "Enhanced empty state with contextual messages"
  [current-status current-page]
  [:> CommandEmpty
   {:className "flex items-center justify-center text-center text-sm text-gray-11 h-full"}
   (case [current-status current-page]
     [:idle :main] "Search for resources, features and more..."
     [:idle :connection-actions] "Choose an action for this connection"
     [:searching :main] "Searching..."
     "No results found.")])

(defn command-palette
  "Main command palette component"
  []
  (let [palette-state (rf/subscribe [:command-palette])
        search-results (rf/subscribe [:command-palette->search-results])
        current-user (rf/subscribe [:users->current-user])]
    (fn []
      (let [status (:status @search-results)
            current-page (:current-page @palette-state)
            context (:context @palette-state)
            user-data (:data @current-user)
            ;; Show subtle search indicator only on icon
            is-searching? (or (= status :searching) (= status :loading))
            ;; Dynamic placeholder based on current page
            placeholder (case current-page
                          :main "Search for resources, features and more..."
                          :connection-actions "Select or search an action"
                          "Search...")]
        [command-dialog/command-dialog
         {:open? (:open? @palette-state)
          :on-open-change #(if %
                             (rf/dispatch [:command-palette->open])
                             (rf/dispatch [:command-palette->close]))
          :title "Command Palette"
          :search-config {:show-search-icon true
                          :show-input true
                          :is-searching? is-searching?
                          :placeholder placeholder
                          :value (:query @palette-state)
                          :on-value-change #(rf/dispatch [:command-palette->search (or % "")])
                          :on-key-down (fn [e]
                                         (when (or (= (.-key e) "Escape")
                                                   (and (= (.-key e) "Backspace")
                                                        (empty? (or (:query @palette-state) ""))))
                                           (when (not= current-page :main)
                                             (.preventDefault e)
                                             (rf/dispatch [:command-palette->back]))))}
          :breadcrumb-config (when (not= current-page :main)
                               {:current-page current-page
                                :context context
                                :on-close #(rf/dispatch [:command-palette->back])})
          :content
          [:<>
           [enhanced-empty-state status current-page]

           ;; Render content based on current page
           (case current-page
             :main
             [pages/main-page @search-results user-data]

             :connection-actions
             [pages/connection-actions-page context user-data]

             ;; Default: main page
             [pages/main-page @search-results user-data])]}]))))

(defn keyboard-listener
  "Component to capture CMD+K / Ctrl+K keyboard shortcuts"
  []
  (r/with-let [handle-keydown (fn [e]
                                (when (and (or (.-metaKey e) (.-ctrlKey e))
                                           (= (.-key e) "k"))
                                  (.preventDefault e)
                                  (rf/dispatch [:command-palette->toggle])))
               _ (js/document.addEventListener "keydown" handle-keydown)]

    nil

    (finally
      (js/document.removeEventListener "keydown" handle-keydown))))
