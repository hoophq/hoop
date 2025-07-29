(ns webapp.features.access-control.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.headings :as h]
   [webapp.components.loaders :as loaders]
   [webapp.features.access-control.views.empty-state :as empty-state]
   [webapp.features.access-control.views.group-list :as group-list]
   [webapp.features.promotion :as promotion]))

(defn main []
  (let [plugin-details (rf/subscribe [:plugins->plugin-details])
        all-groups (rf/subscribe [:access-control/all-groups])
        min-loading-done (r/atom false)]

    (rf/dispatch [:plugins->get-plugin-by-name "access_control"])
    (rf/dispatch [:users->get-user-groups])

    (js/setTimeout #(reset! min-loading-done true) 1500)

    (fn []
      (let [plugin (:plugin @plugin-details)
            installed? (or (:installed? plugin) false)
            has-user-groups? (and @all-groups (seq @all-groups))
            loading? (or (= :loading (:status @plugin-details))
                         (not @min-loading-done))]

        (cond
          loading?
          [:> Box {:class "bg-gray-1 h-full"}
           [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
            [loaders/simple-loader]]]

          (not installed?)
          [:> Box {:class "bg-gray-1 h-full"}
           [promotion/access-control-promotion {:mode :empty-state
                                                :installed? false}]]

          :else
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
             [:> Box {:class "h-full"}
              (if (not has-user-groups?)
                [empty-state/main installed?]
                [group-list/main])]]]])))))
