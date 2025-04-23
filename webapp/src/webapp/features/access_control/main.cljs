(ns webapp.features.access-control.main
  (:require
   ["@radix-ui/themes" :refer [Box Flex Tabs]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.headings :as h]
   [webapp.features.access-control.views.empty-state :as empty-state]
   [webapp.features.access-control.views.group-list :as group-list]))

(defn main []
  (let [active-tab (r/atom "list")
        plugin-details (rf/subscribe [:plugins->plugin-details])
        user-groups (rf/subscribe [:user-groups])]

    ;; Fetch plugin details and user groups when component mounts
    (rf/dispatch [:plugins->get-plugin-by-name "access_control"])
    (rf/dispatch [:users->get-user-groups])

    (fn []
      (let [plugin (:plugin @plugin-details)
            installed? (or (:installed? plugin) false)
            has-user-groups? (and @user-groups (seq @user-groups))]

        [:> Box {:class "flex flex-col bg-gray-1 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
         [:> Flex {:direction "column" :gap "6"}
          [h/h2 "Access Control" {:class "mb-6"}]

          ;; If the plugin is not installed or no groups exist, show empty state
          (if (and installed? has-user-groups?)
            [:> Box
             ;; Tabs for navigation between list and configuration
             [:> Tabs.Root {:default-value "list" :on-value-change #(reset! active-tab %)}
              [:> Tabs.List {:class "mb-8"}
               [:> Tabs.Trigger {:value "list"} "List"]
               [:> Tabs.Trigger {:value "configuration"} "Configuration"]]

              [:> Tabs.Content {:value "list"}
               [group-list/main]]

              [:> Tabs.Content {:value "configuration"}
               [:div "Configuration content will be implemented later"]]]]

            ;; Empty state
            [empty-state/main installed?])]]))))
