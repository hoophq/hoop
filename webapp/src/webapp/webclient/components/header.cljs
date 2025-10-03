(ns webapp.webclient.components.header
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Flex Heading IconButton Tooltip]]
   ["lucide-react" :refer [CircleHelp FastForward PackagePlus Play Sun Moon ChevronDown PanelLeft LayoutList]]
   [re-frame.core :as rf]
   [webapp.components.notification-badge :refer [notification-badge]]
   [webapp.webclient.components.search :as search]))


(defn main []
  (let [metadata (rf/subscribe [:editor-plugin/metadata])
        metadata-key (rf/subscribe [:editor-plugin/metadata-key])
        metadata-value (rf/subscribe [:editor-plugin/metadata-value])
        primary-connection (rf/subscribe [:primary-connection/selected])
        selected-connections (rf/subscribe [:multiple-connections/selected])
        use-compact-ui? (rf/subscribe [:webclient/use-compact-ui?])]
    (fn [active-panel multi-run-panel? dark-mode? submit]
      (let [has-metadata? (or (seq @metadata)
                              (seq @metadata-key)
                              (seq @metadata-value))
            no-connection-selected? (and (empty? @selected-connections)
                                         (not @primary-connection))
            has-multirun? (seq @selected-connections)
            exec-enabled? (= "enabled" (:access_mode_exec @primary-connection))
            disable-run-button? (or (not exec-enabled?)
                                    no-connection-selected?)
            on-click-icon-button (fn [type]
                                   (reset! active-panel (when-not (= @active-panel type) type))
                                   (cond
                                     (= type :connections)
                                     (rf/dispatch [:multiple-connections/clear])))]
        [:> Box {:class "h-16 border-b-2 border-gray-3 bg-gray-1"}
         [:> Flex {:align "center"
                   :justify "between"
                   :class "h-full px-4"}
          [:> Flex {:align "center" :gap "2"}
           [:> Heading {:as "h1" :size "6" :weight "bold" :class "text-gray-12"}
            "Terminal"]

           ;; Badge de conexão (apenas se compact UI)
           (when @use-compact-ui?
             [:> Badge
              {:radius "full"
               :color (if @primary-connection "blue" "gray")
               :class "cursor-pointer"
               :onClick (fn [] (rf/dispatch [:primary-connection/toggle-dialog true]))}
              (if @primary-connection
                (:name @primary-connection)
                "Connection")
              [:> ChevronDown {:size 12}]])]
          [:> Flex {:align "center" :gap "2"}

           (when-not @use-compact-ui?
             [:> Tooltip {:content "Search"}
              [:div
               [search/main active-panel]]])

           [:> Tooltip {:content "Help"}
            [:> IconButton
             {:size "2"
              :color "gray"
              :variant "soft"
              :onClick (fn []
                         (js/window.open "https://help.hoop.dev" "_blank"))}
             [:> CircleHelp {:size 16}]]]

           [:> Tooltip {:content (if @use-compact-ui? "Classic Layout" "Compact Layout")}
            [:> IconButton
             {:class (when @use-compact-ui?
                       "bg-gray-8 text-gray-12")
              :size "2"
              :color "gray"
              :variant "soft"
              :onClick (fn []
                         (let [new-value (not @use-compact-ui?)]
                           (.setItem js/localStorage "compact-terminal-ui" (str new-value))
                           (.reload js/location)))}
             (if @use-compact-ui?
               [:> PanelLeft {:size 16}]
               [:> LayoutList {:size 16}])]]

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
            [:div
             [notification-badge
              {:icon [:> PackagePlus {:size 16}]
               :on-click #(on-click-icon-button :metadata)
               :active? (= @active-panel :metadata)
               :has-notification? has-metadata?
               :disabled? false}]]]

           ;; Botão MultiRun apenas se NÃO for compact UI
           (when-not @use-compact-ui?
             [:> Tooltip {:content "MultiRun"}
              [:div
               [notification-badge
                {:icon [:> FastForward {:size 16}]
                 :on-click #(do
                              (reset! multi-run-panel? (not @multi-run-panel?))
                              (rf/dispatch [:multiple-connections/clear]))
                 :active? @multi-run-panel?
                 :has-notification? has-multirun?
                 :disabled? false}]]])

           [:> Tooltip {:content "Run"}
            [:> Button
             {:disabled disable-run-button?
              :class (when disable-run-button? "cursor-not-allowed")
              :onClick #(submit)}
             [:> Play {:size 16}]
             "Run"]]]]]))))
