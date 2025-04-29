(ns webapp.features.runbooks.main
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text Tabs Separator]]
   [re-frame.core :as rf]
   [webapp.components.headings :as h]
   [webapp.features.runbooks.views.empty-state :as empty-state]
   [webapp.features.runbooks.views.runbook-list :as runbook-list]
   [webapp.features.runbooks.views.configuration-view :as config-view]))

(defn main []
  (let [plugin-details (rf/subscribe [:plugins->plugin-details])
        connections (rf/subscribe [:connections])]

    (rf/dispatch [:plugins->get-plugin-by-name "runbooks"])
    (rf/dispatch [:connections->get-connections])

    (fn []
      (let [plugin (:plugin @plugin-details)
            installed? (or (:installed? plugin) false)
            has-connections? (and (:results @connections) (seq (:results @connections)))]

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
           (if (not installed?)
             [empty-state/main installed?]

             ;; Tabs
             [:> Tabs.Root {:default-value "connections"}
              [:> Tabs.List {:aria-label "Runbooks tabs"}
               [:> Tabs.Trigger {:value "connections"} "Connections"]
               [:> Tabs.Trigger {:value "configuration"} "Configuration"]]

              [:> Separator {:size "4" :mb "7"}]

              [:> Tabs.Content {:value "connections" :class "h-full"}
               (if (not has-connections?)
                 [empty-state/main installed?]
                 [runbook-list/main])]

              [:> Tabs.Content {:value "configuration" :class "h-full"}
               [config-view/main]]])]]]))))
