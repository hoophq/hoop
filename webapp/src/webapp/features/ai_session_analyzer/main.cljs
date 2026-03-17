(ns webapp.features.ai-session-analyzer.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Heading Flex Separator Tabs Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.features.promotion :as promotion]
   [webapp.features.ai-session-analyzer.views.configuration-view :as config-view]
   [webapp.features.ai-session-analyzer.views.empty-state :as empty-state]
   [webapp.features.ai-session-analyzer.views.rule-list :as rule-list]))

(defn- loading-view []
  [:> Flex {:justify "center" :align "center"}
   [loaders/simple-loader {:size "6" :border-size "4"}]])

(defn main []
  (let [rules-data (rf/subscribe [:ai-session-analyzer/rules])
        loading-rules? (rf/subscribe [:ai-session-analyzer/rules-loading])
        loading-config? (rf/subscribe [:ai-session-analyzer/provider-loading])
        provider-data (rf/subscribe [:ai-session-analyzer/provider])
        active-tab (r/atom "rules")
        promotion-seen? (r/atom (boolean (.getItem (.-localStorage js/window) "ai-session-analyzer-promotion-seen")))]

    (rf/dispatch [:ai-session-analyzer/get-rules])
    (rf/dispatch [:ai-session-analyzer/get-provider])

    (fn []
      (let [has-rules? (seq (or (:data @rules-data) []))
            provider-configured? (= :success (:status @provider-data))]

        (if (not @promotion-seen?)
          [:> Box {:class "h-full bg-gray-1"}
           [promotion/ai-session-analyzer-promotion
            {:mode :empty-state
             :on-promotion-seen (fn []
                                  (.setItem (.-localStorage js/window) "ai-session-analyzer-promotion-seen" "true")
                                  (reset! promotion-seen? true))}]]

          [:> Box {:class "h-full flex flex-col bg-gray-1 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
           [:> Flex {:direction "column" :gap "5" :class "h-full"}

            [:> Flex {:justify "between" :align "center"}
             [:> Box
              [:> Heading {:size "8" :weight "bold" :as "h1"}
               "AI Session Analyzer"]
              [:> Text {:as "p" :size "5" :class "text-gray-500 mt-2"}
               "Monitor terminal sessions and resource usage in real time."]]
             (when has-rules?
               [:> Button {:size "3"
                           :variant "solid"
                           :on-click #(rf/dispatch [:navigate :create-ai-session-analyzer-rule])}
                "Create new rule"])]

            [:> Box {:class "flex flex-col"}
             [:> Tabs.Root {:value @active-tab
                            :onValueChange #(reset! active-tab %)}
              [:> Tabs.List {:aria-label "AI Session Analyzer tabs"}
               [:> Tabs.Trigger {:value "rules"} "Rules"]
               [:> Tabs.Trigger {:value "configure"} "Configure"]]

              [:> Separator {:size "4" :mb "7"}]

              [:> Tabs.Content {:value "rules"}
               (cond
                 @loading-rules? [loading-view]
                 (not has-rules?) [empty-state/main
                                   {:provider-configured? provider-configured?
                                    :on-configure #(reset! active-tab "configure")}]
                 :else [rule-list/main])]

              [:> Tabs.Content {:value "configure"}
               (if @loading-config?
                 [loading-view]
                 [config-view/main active-tab])]]]]])))))
