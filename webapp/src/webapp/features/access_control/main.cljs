(ns webapp.features.access-control.main
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text Button]]
   [re-frame.core :as rf]
   [webapp.components.headings :as h]
   [webapp.features.access-control.views.empty-state :as empty-state]
   [webapp.features.access-control.views.group-list :as group-list]))

(defn main []
  (let [plugin-details (rf/subscribe [:plugins->plugin-details])
        user-groups (rf/subscribe [:user-groups])]

    (rf/dispatch [:plugins->get-plugin-by-name "access_control"])
    (rf/dispatch [:users->get-user-groups])

    (fn []
      (let [plugin (:plugin @plugin-details)
            installed? (or (:installed? plugin) false)
            filtered-user-groups (filter #(not= "admin" %) @user-groups)
            has-user-groups? (and filtered-user-groups (seq filtered-user-groups))]

        [:> Box {:class "flex flex-col bg-white px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
         [:> Flex {:direction "column" :gap "6" :class "h-full"}

          [:> Flex {:justify "between" :align "center" :class "mb-6"}
           [:> Box
            [h/h2 "Access Control" {:class "text-[--gray-12]"}]
            [:> Text {:as "p" :size "3" :class "text-gray-500"}
             "Manage which user groups have access to specific connections."]
            [:> Text {:as "p" :size "3" :class "text-gray-500"}
             "Control permissions and enhance security for your organization."]]
           (when (and installed? has-user-groups?)
             [:> Button {:size "3"
                         :onClick #(rf/dispatch [:navigate :access-control-new])}
              "Create Group"])]

          [:> Box {:class "flex-grow"}
           (if (not installed?)
             [empty-state/main installed?]

             [:> Box {:class "h-full"}
              (if (not has-user-groups?)
                [empty-state/main installed?]
                [group-list/main])])]]]))))
