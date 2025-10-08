(ns webapp.webclient.components.header
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Flex Heading IconButton Tooltip]]
   ["lucide-react" :refer [CircleHelp FastForward PackagePlus
                           Play Sun Moon ChevronDown Search]]
   [re-frame.core :as rf]
   [webapp.components.notification-badge :refer [notification-badge]]))


(defn main []
  (let [metadata (rf/subscribe [:editor-plugin/metadata])
        metadata-key (rf/subscribe [:editor-plugin/metadata-key])
        metadata-value (rf/subscribe [:editor-plugin/metadata-value])
        primary-connection (rf/subscribe [:primary-connection/selected])
        selected-connections (rf/subscribe [:multiple-connections/selected])
        active-panel (rf/subscribe [:webclient->active-panel])]
    (fn [dark-mode? submit]
      (let [has-metadata? (or (seq @metadata)
                              (seq @metadata-key)
                              (seq @metadata-value))
            no-connection-selected? (and (empty? @selected-connections)
                                         (not @primary-connection))
            has-multirun? (seq @selected-connections)
            exec-enabled? (= "enabled" (:access_mode_exec @primary-connection))
            disable-run-button? (or (not exec-enabled?)
                                    no-connection-selected?)]
        [:> Box {:class "h-16 border-b-2 border-gray-3 bg-gray-1"}
         [:> Flex {:align "center"
                   :justify "between"
                   :class "h-full px-4"}
          [:> Flex {:align "center" :gap "2"}
           [:> Heading {:as "h1" :size "6" :weight "bold" :class "text-gray-12"}
            "Terminal"]

           [:> Badge
            {:radius "full"
             :color (if @primary-connection "indigo" "gray")
             :class "cursor-pointer"
             :onClick (fn []
                        (rf/dispatch [:connections->get-connections])
                        (rf/dispatch [:primary-connection/toggle-dialog true]))}
            (if @primary-connection
              (:name @primary-connection)
              "Connection")
            [:> ChevronDown {:size 12}]]]
          [:> Flex {:align "center" :gap "2"}

           [:> Tooltip {:content "Search"}
            [:> IconButton
             {:size "2"
              :variant "soft"
              :color "gray"
              :onClick (fn []
                         (rf/dispatch [:connections->get-connections])
                         (rf/dispatch [:primary-connection/toggle-dialog true]))}
             [:> Search {:size 16}]]]

           [:> Tooltip {:content "Help"}
            [:> IconButton
             {:size "2"
              :color "gray"
              :variant "soft"
              :onClick (fn []
                         (js/window.open "https://help.hoop.dev" "_blank"))}
             [:> CircleHelp {:size 16}]]]


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
               :on-click #(rf/dispatch [:webclient/set-active-panel :metadata])
               :active? (= @active-panel :metadata)
               :has-notification? has-metadata?
               :disabled? false}]]]

           [:> Tooltip {:content "MultiRun"}
            [:div
             [notification-badge
              {:icon [:> FastForward {:size 16}]
               :on-click (fn []
                           (if @primary-connection
                             (rf/dispatch [:webclient/set-active-panel :multiple-connections])

                             (do
                               (rf/dispatch [:connections->get-connections])
                               (rf/dispatch [:primary-connection/toggle-dialog true]))))
               :active? (= @active-panel :multiple-connections)
               :has-notification? has-multirun?
               :disabled? false}]]]

           [:> Tooltip {:content "Run"}
            [:> Button
             {:disabled disable-run-button?
              :class (when disable-run-button? "cursor-not-allowed")
              :onClick #(submit)}
             [:> Play {:size 16}]
             "Run"]]]]]))))
