(ns webapp.features.access-control.main
  (:require
   ["@radix-ui/themes" :refer [Box Flex Tabs]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.headings :as h]
   [webapp.features.access-control.views.empty-state :as empty-state]
   [webapp.features.access-control.views.group-list :as group-list]
   [webapp.features.access-control.views.configuration :as configuration]))

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
          [h/h2 "Access Control" {:class "mb-2"}]

          ;; If the plugin is not installed, show empty state
          (if (not installed?)
            [empty-state/main installed?]

            ;; Otherwise show tabs and content
            [:> Box
             ;; Tabs for navigation between list and configuration
             [:> Tabs.Root {:default-value "list"
                            :value @active-tab
                            :on-value-change #(reset! active-tab %)}
              [:> Tabs.List {:class "mb-8"}
               [:> Tabs.Trigger {:value "list"} "List"]
               [:> Tabs.Trigger {:value "configuration"} "Configuration"]]

              [:> Tabs.Content {:value "list"}
               ;; If there are no groups but plugin is installed, show empty state for groups
               (if (not has-user-groups?)
                 [empty-state/main installed?]
                 [group-list/main])]

              [:> Tabs.Content {:value "configuration"}
               [configuration/main]]]])]]))))
