
(ns webapp.webclient.components.header
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading IconButton]]
   ["lucide-react" :refer [BookUp2 FastForward PackagePlus Play Sun]]
   [re-frame.core :as rf]
   [reagent.core :as r]
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
  (let [active-tool (r/atom nil)
        selected-template (rf/subscribe [:runbooks-plugin->selected-runbooks])
        metadata (rf/subscribe [:editor-plugin/metadata])
        metadata-key (rf/subscribe [:editor-plugin/metadata-key])
        metadata-value (rf/subscribe [:editor-plugin/metadata-value])
        selected-connections (rf/subscribe [:connection-selection/selected])]
    (fn [active-panel multi-run-panel? dark-mode? submit]
      (let [has-runbook? (some? (:data @selected-template))
            has-metadata? (or (seq @metadata)
                              (not (empty? @metadata-key))
                              (not (empty? @metadata-value)))
            has-multirun? (seq @selected-connections)
            on-click-icon-button (fn [type]
                                   (reset! active-panel (when-not (= @active-panel type) type))
                                   (when (= type :connections)
                                     (rf/dispatch [:connection-selection/clear])))]
        [:> Box {:class "h-16 border-b-2 border-gray-3 bg-gray-1"}
         [:> Flex {:align "center"
                   :justify "between"
                   :class "h-full px-4"}
          [:> Heading {:as "h1" :size "6" :weight "bold" :class "text-gray-12"}
           "Terminal"]
          [:> Flex {:align "center" :gap "2"}
           #_[:> IconButton
              {:class (when (= @active-tool :search)
                        "bg-gray-8 text-gray-12")
               :size "2"
               :color "gray"
               :variant "soft"
               :onClick #(reset! active-tool :search)}
              [:> Search {:size 16}]]

           [search/main]

           [:> IconButton
            {:class (when @dark-mode?
                      "bg-gray-8 text-gray-12")
             :size "2"
             :color "gray"
             :variant "soft"
             :onClick #(swap! dark-mode? not)}
            [:> Sun {:size 16}]]

           [notification-badge
            [:> BookUp2 {:size 16}]
            #(on-click-icon-button :runbooks)
            (= @active-panel :runbooks)
            has-runbook?]

           [notification-badge
            [:> PackagePlus {:size 16}]
            #(on-click-icon-button :metadata)
            (= @active-panel :metadata)
            has-metadata?]

           [notification-badge
            [:> FastForward {:size 16}]
            #(do
               (reset! multi-run-panel? (not @multi-run-panel?))
               (rf/dispatch [:connection-selection/clear]))
            @multi-run-panel?
            has-multirun?]

           [:> Button
            {:disabled false
             :onClick #(submit)}
            [:> Play {:size 16}]
            "Run"]]]]))))
