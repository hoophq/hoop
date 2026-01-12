(ns webapp.features.runbooks.runner.main
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Flex Heading IconButton ScrollArea Text Tooltip]]
   ["lucide-react" :refer [LibraryBig PackagePlus Play Sun Moon ChevronDown ChevronsLeft ChevronsRight]]
   ["allotment" :refer [Allotment]]
   [reagent.core :as r]
   [re-frame.core :as rf]
   [cljs.core :as c]
   [clojure.string :as cs]
   [webapp.components.notification-badge :refer [notification-badge]]
   [webapp.webclient.components.search :as search]
   [webapp.components.keyboard-shortcuts :refer [detect-os]]
   [webapp.features.runbooks.runner.views.metadata-panel :as metadata-panel]
   [webapp.webclient.log-area.main :as log-area]
   [webapp.webclient.panel :refer [discover-connection-type]]
   [webapp.features.runbooks.runner.views.connections-dialog :as connections-dialog]
   [webapp.features.runbooks.runner.views.list :as runbooks-list]
   [webapp.features.runbooks.runner.views.form :as runbook-form]
   [webapp.parallel-mode.components.header-button :as parallel-mode-button]
   [webapp.parallel-mode.components.modal.main :as parallel-mode-modal]
   [webapp.parallel-mode.components.execution-summary.main :as execution-summary]))

(defn header []
  (let [selected-template (rf/subscribe [:runbooks->selected-runbooks])
        metadata (rf/subscribe [:runbooks/metadata])
        metadata-key (rf/subscribe [:runbooks/metadata-key])
        metadata-value (rf/subscribe [:runbooks/metadata-value])
        runbooks-connection (rf/subscribe [:runbooks/selected-connection])
        script-response (rf/subscribe [:runbooks->exec])]
    (fn [{:keys [dark-mode? metadata-open? toggle-metadata-open]}]
      (let [template @selected-template
            connection @runbooks-connection
            run-disabled? (or (nil? connection)
                              (empty? (:data template)))
            has-metadata? (or (seq @metadata)
                              (seq @metadata-key)
                              (seq @metadata-value))
            runbook-loading? (= (:status @script-response) :loading)
            os (detect-os)]
        (r/with-let [handle-keydown (fn [e]
                                      (when (and (= "Enter" (.-key e))
                                                 (or (.-metaKey e) (.-ctrlKey e))
                                                 (let [current-template @selected-template
                                                       current-connection @runbooks-connection]
                                                   (not (or (nil? current-connection)
                                                            (empty? (:data current-template))))))
                                        (.preventDefault e)
                                        (when-let [form (.getElementById js/document "runbook-form")]
                                          (.requestSubmit form))))
                     _ (.addEventListener js/document "keydown" handle-keydown)]
          [:> Box {:class "h-16 border-b-2 border-gray-3 bg-gray-1"}
           [:> Flex {:class "h-full px-4 items-center justify-between"}
            [:> Flex {:class "items-center gap-2"}
             [:> Heading {:as "h1" :size "6" :weight "bold" :class "text-gray-12"}
              "Runbooks"]
             [:> Badge
              {:radius "full"
               :color (if @runbooks-connection "indigo" "gray")
               :class "cursor-pointer"
               :onClick (fn []
                          (rf/dispatch [:connections/get-connections-paginated {:page 1 :force-refresh? true}])
                          (rf/dispatch [:runbooks/toggle-connection-dialog true]))}
              (if @runbooks-connection
                (:name @runbooks-connection)
                "Resource Role")
              [:> ChevronDown {:size 12}]]]

            [:> Flex {:class "items-center gap-2"}
             [:> Tooltip {:content "Search"}
              [search/main :runbooks]]

             [:> Tooltip {:content "Theme"}
              [:> IconButton
               {:class (when @dark-mode?
                         "bg-gray-8 text-gray-12")
                :size "2"
                :color "gray"
                :variant "soft"
                :onClick (fn []
                           (swap! dark-mode? not)
                           (.setItem js/localStorage "dark-mode" (str @dark-mode?)))}
               (if @dark-mode?
                 [:> Sun {:size 16}]
                 [:> Moon {:size 16}])]]

             [:> Tooltip {:content "Metadata"}
              [notification-badge
               {:icon [:> PackagePlus {:size 16}]
                :on-click toggle-metadata-open
                :active? metadata-open?
                :has-notification? has-metadata?
                :disabled? false}]]

             ;; Parallel Mode Button
             [parallel-mode-button/parallel-mode-button]

             [:> Tooltip {:content (if (= os :mac) "cmd + Enter" "ctrl + Enter")}
              [:> Button
               {:form "runbook-form"
                :type "submit"
                :disabled run-disabled?
                :loading runbook-loading?
                :class (when run-disabled? "cursor-not-allowed")}
               [:> Play {:size 16}]
               "Run"]]]]]
          (finally
            (.removeEventListener js/document "keydown" handle-keydown)))))))

(defn runbooks-library []
  (let [templates (rf/subscribe [:runbooks/runner-data])
        filtered-templates (rf/subscribe [:runbooks->filtered-runbooks])]
    (fn [{:keys [collapsed? on-toggle-collapse]}]
      [:> Box {:as "aside"
               :class (str "h-full flex flex-col transition-all duration-300 border-r-2 border-gray-3 bg-gray-1 "
                           (if collapsed? "w-16" "w-full"))}
       [:> Flex {:align "center"
                 :justify "between"
                 :class "w-full h-10 p-2 border-b border-gray-3"}
        [:> Flex {:align "center" :gap "2"}
         [:> LibraryBig {:size 16 :class "text-[--gray-12]"}]
         [:> Box {:class (when collapsed? "hidden")}
          [:> Heading {:size "3" :weight "bold" :class "text-gray-12"} "Library"]]]
        [:> IconButton {:variant "ghost"
                        :color "gray"
                        :onClick on-toggle-collapse}
         [:> (if collapsed? ChevronsRight ChevronsLeft) {:size 16}]]]
       (when-not collapsed?
         [:> ScrollArea {:class "flex-1"}
          [:> Box {:class "h-full p-2 pb-4"}
           [runbooks-list/main templates filtered-templates]]])])))

(defn main []
  (let [templates (rf/subscribe [:runbooks/runner-data])
        selected-template (rf/subscribe [:runbooks->selected-runbooks])
        search-term (rf/subscribe [:search/term])
        runbooks-connection (rf/subscribe [:runbooks/selected-connection])
        collapsed? (r/atom false)
        metadata-open? (r/atom false)
        dark-mode? (r/atom (= (.getItem js/localStorage "dark-mode") "true"))
        x-panel-sizes (mapv js/parseInt
                            (cs/split
                             (or (.getItem js/localStorage "runbook-x-panel-sizes") "270,950") ","))
        y-panel-sizes (mapv js/parseInt
                            (cs/split
                             (or (.getItem js/localStorage "runbook-y-panel-sizes") "650,210") ","))]

    (rf/dispatch [:runbooks/load-persisted-connection])

    (fn []
      (when (and (seq @search-term)
                 (= :success (:status @templates)))
        (rf/dispatch [:search/filter-runbooks @search-term]))

      [:> Box {:class (str "h-full bg-gray-2 overflow-hidden " (when @dark-mode? "dark"))}
       [parallel-mode-modal/parallel-mode-modal]
       [execution-summary/execution-summary-modal]
       
       [header {:dark-mode? dark-mode?
                :metadata-open? @metadata-open?
                :toggle-metadata-open #(swap! metadata-open? not)}]
       [:> Allotment {:key "outer-allotment"
                      :horizontal true
                      :separator false}
        [:> Flex {:class "h-[calc(100%-4rem)]"}
         [:> Allotment {:key (str "main-allotment-" @collapsed?)
                        :defaultSizes (if @collapsed? [64 950] x-panel-sizes)
                        :onDragEnd #(.setItem js/localStorage "runbook-x-panel-sizes" (str %))
                        :horizontal true
                        :separator false}
          [:> (.-Pane Allotment) {:minSize (if @collapsed? 64 270)
                                  :maxSize (if @collapsed? 64 1000)}
           [runbooks-library {:collapsed? @collapsed? :on-toggle-collapse #(swap! collapsed? not)}]]
          [:> Allotment {:defaultSizes y-panel-sizes
                         :onDragEnd #(.setItem js/localStorage "runbook-y-panel-sizes" (str %))
                         :vertical true
                         :separator false}
           [:> Box {:class "h-full flex-1"}
            [connections-dialog/connections-dialog]
            [runbook-form/main {:runbook @selected-template
                                :selected-connection @runbooks-connection}]]
           [:> Flex {:direction "column" :justify "between" :class "h-full border-t border-gray-3"}
            [log-area/main
             (discover-connection-type @runbooks-connection)
             true
             @dark-mode?]]]]]
        (when @metadata-open?
          [:> (.-Pane Allotment) {:minSize 250 :maxSize 370}
           [:> Box {:class "h-full w-full bg-gray-1 border-l border-gray-3 overflow-y-auto"}
            [:> Flex {:justify "between"
                      :align "center"
                      :class "px-4 py-3 border-b border-gray-3"}
             [:> Text {:size "3" :weight "bold" :class "text-gray-12"} "Metadata"]]
            [:> Box {:class "p-4"}
             [metadata-panel/main]]]])]])))

