(ns webapp.webclient.components.header
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading IconButton Tooltip]]
   ["lucide-react" :refer [CircleHelp PackagePlus ChevronDown
                           Play Sun Moon Search]]
   [re-frame.core :as rf]
   [webapp.components.notification-badge :refer [notification-badge]]
   [webapp.components.keyboard-shortcuts :refer [detect-os]]
   [webapp.components.skip-link :as skip-link]
   [webapp.parallel-mode.components.header-button :as parallel-mode-button]
   [webapp.webclient.components.features-indicator :as features-indicator]))


(defn main []
  (let [metadata (rf/subscribe [:editor-plugin/metadata])
        metadata-key (rf/subscribe [:editor-plugin/metadata-key])
        metadata-value (rf/subscribe [:editor-plugin/metadata-value])
        primary-connection (rf/subscribe [:primary-connection/selected])
        active-panel (rf/subscribe [:webclient->active-panel])
        script-response (rf/subscribe [:editor-plugin->script])
        parallel-mode-active? (rf/subscribe [:parallel-mode/is-active?])]
    (fn [dark-mode? submit]
      (let [has-metadata? (or (seq @metadata)
                              (seq @metadata-key)
                              (seq @metadata-value))
            connection-selected? (or @parallel-mode-active?
                                     (boolean @primary-connection))
            exec-enabled? (= "enabled" (:access_mode_exec @primary-connection))
            disable-run-button? (not (or exec-enabled?
                                         connection-selected?))
            script-loading? (= (:status @script-response) :loading)
            os (detect-os)]
        ;; Sampled off the Figma top bar: #e5e5e5 surface over a 2px #90909c
        ;; rule, which on the Radix scale the legacy app uses is gray-4 and
        ;; gray-9. The old bg-gray-1/border-gray-3 pairing was near-white on
        ;; near-white and read as no bar at all under the new global header.
        [:> Box {:class "h-10 shrink-0 border-b-2 border-gray-9 bg-gray-4"}
         [:> Flex {:align "center"
                   :justify "between"
                   :class "h-full px-4"}
          [:> Flex {:align "center" :gap "4"}
           [:> Heading {:as "h1" :size "3" :weight "bold" :class "text-gray-12"}
            "Terminal"]

           [:> Button
            {:radius "full"
             :size "1"
             :variant (if (and @primary-connection
                                (not @parallel-mode-active?))
                         "solid"
                         "soft")
             :color (if (and @primary-connection
                             (not @parallel-mode-active?))
                      "indigo"
                      "gray")
             :disabled @parallel-mode-active?
             :class (str "gap-1 " (when @parallel-mode-active? "cursor-not-allowed"))
             :aria-label (cond
                           (and @primary-connection
                                (not @parallel-mode-active?))
                           (str "Selected a resource role: " (:name @primary-connection) ". Click to change")
                           @parallel-mode-active?
                           "Resource Roles - parallel mode active"
                           :else
                           "Select a resource role")
             :onClick (when-not @parallel-mode-active?
                        (fn []
                          (rf/dispatch [:connections/get-connections-paginated {:page 1 :force-refresh? true}])
                          (rf/dispatch [:primary-connection/toggle-dialog true])))}
            (cond
              (and @primary-connection
                   (not @parallel-mode-active?)) (:name @primary-connection)
              @parallel-mode-active? "Resource Roles"
              :else "Resource Role")
            [:> ChevronDown {:size 12 :aria-hidden "true"}]]

           ;; Sits beside the resource role rather than with the toolbar icons
           ;; on the right: it describes that role, not the editor. Hidden when
           ;; the role has no feature active.
           [features-indicator/main]

           ;; Skip link: Resource Role → Editor
           [skip-link/main
            {:target-selector "[tabindex='0'][aria-label*='Script editor']"
             :text "Skip to editor"
             :position "focus:left-4"}]]
          [:> Flex {:align "center" :gap "4"}

           [:> Tooltip {:content "Search"}
            [:> IconButton
             {:size "1"
              :variant "ghost"
              :color "gray"
              :highContrast true
              :aria-label "Search resource roles"
              :onClick (fn []
                         (rf/dispatch [:connections/get-connections-paginated {:page 1 :force-refresh? true}])
                         (rf/dispatch [:primary-connection/toggle-dialog true]))}
             [:> Search {:size 16}]]]

           [:> Tooltip {:content "Help"}
            [:> IconButton
             {:size "1"
              :color "gray"
              :variant "ghost"
              :highContrast true
              :aria-label "Open help documentation"
              :onClick (fn []
                         (js/window.open "https://help.hoop.dev" "_blank"))}
             [:> CircleHelp {:size 16}]]]


           [:> Tooltip {:content "Theme"}
            [:> IconButton
             {:class (when @dark-mode?
                       "bg-gray-8 text-gray-12")
              :size "1"
              :color "gray"
              :variant "ghost"
              :highContrast true
              :aria-label (if @dark-mode?
                            "Switch to light theme"
                            "Switch to dark theme")
              :onClick (fn []
                         (swap! dark-mode? not)
                         (.setItem js/localStorage "dark-mode" (str @dark-mode?)))}
             (if @dark-mode?
               [:> Sun {:size 16}]
               [:> Moon {:size 16}])]]

           [:> Tooltip {:content "Metadata"}
            ;; inline-flex: this wrapper only exists to give Tooltip a ref, and
            ;; as a block it added its own line box around the ghost button.
            [:div {:class "inline-flex"}
             [notification-badge
              {:size "1"
               :variant "ghost"
               :icon [:> PackagePlus {:size 16}]
               :on-click #(rf/dispatch [:webclient/set-active-panel :metadata])
               :active? (= @active-panel :metadata)
               :has-notification? has-metadata?
               :disabled? false
               :aria-label "Toggle metadata panel"
               :aria-expanded (= @active-panel :metadata)}]]]

           ;; New Parallel Mode Button
           [parallel-mode-button/parallel-mode-button {:size "1" :variant "ghost"}]

           [:> Tooltip {:content (if (= os :mac) "cmd + Enter" "ctrl + Enter")}
            [:> Button
             {:size "1"
              :disabled disable-run-button?
              :loading script-loading?
              :id "run-button"
              :data-run-button true
              :class (when (or disable-run-button?
                               script-loading?) "cursor-not-allowed")
              :aria-label (str "Run script" (if (= os :mac) " (Cmd+Enter)" " (Ctrl+Enter)"))
              :onClick (fn []
                         (when-not script-loading?
                           (submit)))}
             [:> Play {:size 16}]
             "Run"]]]]]))))
