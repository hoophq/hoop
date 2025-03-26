(ns webapp.webclient.components.header
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading IconButton Tooltip]]
   ["lucide-react" :refer [BookUp2 FastForward PackagePlus Play Sun Moon]]
   [re-frame.core :as rf]
   [webapp.webclient.components.search :as search]))

(defn notification-badge [icon on-click active? has-data?]
  [:div {:class "relative"}
   [:> IconButton
    {:class (when active? "bg-gray-8 text-gray-12")
     :size "2"
     :color "gray"
     :variant "soft"
     :on-click on-click}
    icon]
   (when has-data?
     [:div {:class (str "absolute -top-1 -right-1 w-2 h-2 "
                        "rounded-full bg-red-500")}])])

(defn main []
  (let [selected-template (rf/subscribe [:runbooks-plugin->selected-runbooks])
        metadata (rf/subscribe [:editor-plugin/metadata])
        metadata-key (rf/subscribe [:editor-plugin/metadata-key])
        metadata-value (rf/subscribe [:editor-plugin/metadata-value])
        primary-connection (rf/subscribe [:connections/selected])
        selected-connections (rf/subscribe [:connection-selection/selected])]
    (fn [active-panel multi-run-panel? dark-mode? submit]
      (let [has-runbook? (some? (:data @selected-template))
            has-metadata? (or (seq @metadata)
                              (not (empty? @metadata-key))
                              (not (empty? @metadata-value)))
            has-multirun? (seq @selected-connections)
            on-click-icon-button (fn [type]
                                   (reset! active-panel (when-not (= @active-panel type) type))
                                   (cond
                                     (= type :connections)
                                     (rf/dispatch [:connection-selection/clear])

                                     (= type :runbooks)
                                     (rf/dispatch [:runbooks-plugin->get-runbooks
                                                   (map :name (concat
                                                               (when @primary-connection [@primary-connection])
                                                               @selected-connections))])))]
        [:> Box {:class "h-16 border-b-2 border-gray-3 bg-gray-1"}
         [:> Flex {:align "center"
                   :justify "between"
                   :class "h-full px-4"}
          [:> Heading {:as "h1" :size "6" :weight "bold" :class "text-gray-12"}
           "Terminal"]
          [:> Flex {:align "center" :gap "2"}

           [:> Tooltip {:content "Search (⌘K / Ctrl+K)"}
            [:div
             [search/main active-panel]]]

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

           [:> Tooltip {:content "Runbooks"}
            [:div
             [notification-badge
              [:> BookUp2 {:size 16}]
              #(on-click-icon-button :runbooks)
              (= @active-panel :runbooks)
              has-runbook?]]]

           [:> Tooltip {:content "Metadata"}
            [:div
             [notification-badge
              [:> PackagePlus {:size 16}]
              #(on-click-icon-button :metadata)
              (= @active-panel :metadata)
              has-metadata?]]]

           [:> Tooltip {:content "MultiRun"}
            [:div
             [notification-badge
              [:> FastForward {:size 16}]
              #(do
                 (reset! multi-run-panel? (not @multi-run-panel?))
                 (rf/dispatch [:connection-selection/clear]))
              @multi-run-panel?
              has-multirun?]]]

           [:> Tooltip {:content "Run"}
            [:> Button
             {:disabled false
              :onClick #(submit)}
             [:> Play {:size 16}]
             "Run"]]]]]))))
