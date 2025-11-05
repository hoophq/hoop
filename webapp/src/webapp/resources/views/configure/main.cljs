(ns webapp.resources.views.configure.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Tabs Text]]
   ["lucide-react" :refer [Plus]]
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.components.loaders :as loaders]
   [webapp.connections.constants :as constants]
   [webapp.resources.views.configure.information-tab :as information-tab]
   [webapp.resources.views.configure.roles-tab :as roles-tab]))

(defn header [resource]
  (let [user @(rf/subscribe [:user])]
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
         (:name resource)]]]

      ;; Add New Role button (admin only)
      [:> Button {:size "2"
                  :variant "solid"
                  :on-click #(rf/dispatch [:navigate :add-role-to-resource {} :resource-id (:name resource)])}
       [:> Plus {:size 16}]
       "Add New Role"]]]))

(defn loading-view []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn main [resource-id]
  (r/with-let
    [;; Read query param ?tab=roles
     query-params (js->clj (new js/URLSearchParams (.. js/window -location -search))
                           :keywordize-keys true)
     initial-tab (get query-params :tab "information")
     active-tab (r/atom initial-tab)
     resource-details (rf/subscribe [:resources->resource-details])
     _ (rf/dispatch [:resources->get-resource-details resource-id])
     _ (rf/dispatch [:add-role->clear])]

    (let [resource-state @resource-details
          resource (:data resource-state)
          loading? (:loading resource-state)]

      [:> Box {:class "bg-[--gray-1] px-4 py-10 sm:px-6 lg:px-20 lg:pt-6 lg:pb-10 h-full overflow-auto"}
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

