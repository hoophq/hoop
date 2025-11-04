(ns webapp.resources.views.configure.main
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Tabs Text]]
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.components.loaders :as loaders]
   [webapp.connections.constants :as constants]
   [webapp.resources.views.configure.information-tab :as information-tab]
   [webapp.resources.views.configure.roles-tab :as roles-tab]))

(defn header [resource]
  [:> Box {:class "pb-[--space-5]"}
   [:> Flex {:justify "between" :align "center"}
    [:> Box {:class "space-y-radix-3"}
     [:> Heading {:size "6" :weight "bold" :class "text-[--gray-12]"} "Configure Resource"]
     [:> Flex {:gap "3" :align "center"}
      [:figure {:class "w-4"}
       [:img {:src (constants/get-connection-icon
                    {:type (:type resource)
                     :subtype (:subtype resource)})}]]
      [:> Text {:size "3" :class "text-[--gray-12]"}
       (:name resource)]]]]])

(defn loading-view []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn main [resource-id]
  (r/with-let
    [active-tab (r/atom "information")
     resource-details (rf/subscribe [:resources->resource-details])
     _ (rf/dispatch [:resources->get-resource-details resource-id])]

    (let [resource-state @resource-details
          resource (:data resource-state)
          loading? (:loading resource-state)]

      [:> Box {:class "bg-[--gray-2] px-4 py-10 sm:px-6 lg:px-20 lg:pt-6 lg:pb-10 h-full overflow-auto"}
       (if (or loading? (nil? resource))
         [loading-view]
         [:> Box
          ;; Header
          [header resource]

          ;; Tabs
          [:> Tabs.Root {:value @active-tab
                         :onValueChange #(reset! active-tab %)}
           [:> Tabs.List {:size "2"}
            [:> Tabs.Trigger {:value "information"}
             "Resource Information"]
            [:> Tabs.Trigger {:value "roles"}
             "Resource Roles"]]

           [:> Box {:class "mt-6"}
            [:> Tabs.Content {:value "information"}
             [information-tab/main resource]]

            [:> Tabs.Content {:value "roles"}
             [roles-tab/main resource-id]]]]])])))

