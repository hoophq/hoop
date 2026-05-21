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
     [:idle :resource-roles] "Select a role from this resource"
     [:idle :connection-actions] "Choose an action for this resource role"
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
                          :resource-roles "Select or search a role"
                          :connection-actions "Select or search an action"
                          "Search...")
            ;; Enable native filtering for non-main pages (client-side search)
            should-filter? (not= current-page :main)]
        [command-dialog/command-dialog
         {:open? (:open? @palette-state)
          :on-open-change #(if %
                             (rf/dispatch [:command-palette->open])
                             (rf/dispatch [:command-palette->close]))
          :title "Command Palette"
          :should-filter? should-filter?
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

             :resource-roles
             [pages/resource-roles-page context user-data]

             :connection-actions
             [pages/connection-actions-page context user-data]

             ;; Default: main page
             [pages/main-page @search-results user-data])]}]))))

(defn keyboard-listener
  "Component to capture CMD+K / Ctrl+K keyboard shortcuts.

   When the page is wrapped by the React shell (webapp_v2), this Reagent
   component stays mounted even while its DOM is parked, so the listener
   keeps firing on React-only routes. Without the visibility guard,
   :command-palette->toggle would open the CLJS Radix dialog as a second
   body-level portal alongside the Mantine Spotlight and steal focus.

   The two window globals are set by webapp_v2:
     __hoopReactShellPresent      — true when the React shell is the host
     __hoopReactShellCljsVisible  — true while ClojureApp is mounted
   Legacy CLJS-only mode leaves both undefined, so the handler runs as before."
  []
  (r/with-let [handle-keydown (fn [e]
                                (when (and (or (.-metaKey e) (.-ctrlKey e))
                                           (= (.-key e) "k")
                                           (or (not (.-__hoopReactShellPresent js/window))
                                               (.-__hoopReactShellCljsVisible js/window)))
                                  (.preventDefault e)
                                  (rf/dispatch [:command-palette->toggle])))
               _ (js/document.addEventListener "keydown" handle-keydown)]

    nil

    (finally
      (js/document.removeEventListener "keydown" handle-keydown))))
