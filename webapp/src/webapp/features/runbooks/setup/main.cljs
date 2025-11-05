(ns webapp.features.runbooks.setup.main
  (:require
   ["@radix-ui/themes" :refer [Box Flex Separator Tabs Text]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.headings :as h]
   [webapp.features.promotion :as promotion]
   [webapp.features.runbooks.setup.views.configuration-view :as config-view]
   [webapp.features.runbooks.setup.views.empty-state :as empty-state]
   [webapp.features.runbooks.setup.views.runbook-list :as runbook-list]))

(defn parse-params [params]
  (let [params (js/decodeURIComponent params)
        params (cs/split params "=")
        params (second params)]
    params))

(defn main []
  (let [plugin-details (rf/subscribe [:plugins->plugin-details])
        connections (rf/subscribe [:connections->pagination])
        active-tab (r/atom "connections")
        params (.-search (.-location js/window))
        url-tab (r/atom (parse-params params))]

    (rf/dispatch [:plugins->get-plugin-by-name "runbooks"])

    (fn []
      (let [plugin (:plugin @plugin-details)
            installed? (or (:installed? plugin) false)
            has-connections? (and (:data @connections) (seq (:data @connections)))]

        ;; Initialize active tab from URL
        (when @url-tab
          (reset! active-tab @url-tab)
          (reset! url-tab nil))

        (if (and
             (or (not installed?)
                 (empty? (:config plugin)))
             (not (boolean
                   (.getItem (.-localStorage js/window) "runbooks-promotion-seen"))))
          [:> Box {:class "bg-gray-1 h-full"}
           [promotion/runbooks-promotion {:mode :empty-state
                                          :installed? false}]]

          [:> Box {:class "flex flex-col bg-white px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
           [:> Flex {:direction "column" :gap "5" :class "h-full"}

            ;; Cabeçalho
            [:> Flex {:justify "between" :align "center"}
             [:> Box
              [h/h2 "Runbooks" {:class "text-[--gray-12]"}]
              [:> Text {:as "p" :size "3" :class "text-gray-500"}
               "Manage which paths are accessible for each connection."]
              [:> Text {:as "p" :size "3" :class "text-gray-500"}
               "Configure Git repositories and enhance automation for your organization."]]
             nil]

            [:> Box {:class "flex-grow"}
             ;; Tabs
             [:> Tabs.Root {:value @active-tab
                            :onValueChange #(reset! active-tab %)}
              [:> Tabs.List {:aria-label "Runbooks tabs"}
               [:> Tabs.Trigger {:value "connections"} "Connections"]
               [:> Tabs.Trigger {:value "configurations"} "Configurations"]]

              [:> Separator {:size "4" :mb "7"}]

              [:> Tabs.Content {:value "connections" :class "h-full"}
               (if (not has-connections?)
                 [empty-state/main installed?]
                 [runbook-list/main])]

              [:> Tabs.Content {:value "configurations" :class "h-full"}
               [config-view/main active-tab]]]]]])))))
