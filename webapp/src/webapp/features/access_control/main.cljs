(ns webapp.features.access-control.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text Heading]]
   [re-frame.core :as rf]
   [reagent.core :as r]
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
          [:> Box {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
           [:> Flex {:direction "column" :gap "6" :class "h-full"}

            [:> Flex {:justify "between" :align "center" :class "mb-6"}
             [:> Box
              [:> Heading {:size "8" :weight "bold" :as "h1"}
               "Access Control"]
              [:> Text {:size "5" :class "text-[--gray-11]"}
               "Manage which user groups have access to specific resource roles."]
              [:> Text {:as "p" :size "5" :class "text-[--gray-11]"}
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
