(ns webapp.resources.configure.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Tabs Text]]
   ["lucide-react" :refer [Plus]]
   [clojure.string :as cs]
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.components.loaders :as loaders]
   [webapp.connections.constants :as constants]
   [webapp.connections.views.setup.page-wrapper :as page-wrapper]
   [webapp.resources.configure.information-tab :as information-tab]
   [webapp.resources.configure.roles-tab :as roles-tab]))

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
       (:name resource)]]]

    ;; Add New Role button (admin only)
    [:> Button {:size "2"
                :variant "solid"
                :on-click #(rf/dispatch [:navigate :add-role-to-resource {} :resource-id (:name resource)])}
     [:> Plus {:size 16}]
     "Add New Role"]]])

(defn- loading-view []
  [:> Box {:class "flex items-center justify-center h-full"}
   [loaders/simple-loader]])

(defn main [resource-id]
  (let [search-string (.. js/window -location -search)
        url-params (new js/URLSearchParams search-string)
        initial-tab (.get url-params "tab")
        active-tab (r/atom (or initial-tab "information"))
        resource-details (rf/subscribe [:resources->resource-details])
        updating? (rf/subscribe [:resources->updating?])
        new-name (r/atom nil)]

    (rf/dispatch-sync [:resources->get-resource-details resource-id])
    (rf/dispatch [:add-role->clear])
    (rf/dispatch [:connections->load-metadata])

    (fn []
        (let [resource (:data @resource-details)
              loading? (:loading @resource-details)
              handle-save (fn []
                           (when (and resource
                                      (not (cs/blank? @new-name))
                                      (not= @new-name (:name resource)))
                             (rf/dispatch [:resources->update-resource-name (:name resource) @new-name])))
              handle-delete (fn []
                             (rf/dispatch [:dialog->open
                                           {:title "Delete resource?"
                                            :type :danger
                                            :text-action-button "Confirm and delete"
                                            :action-button? true
                                            :text [:> Box {:class "space-y-radix-4"}
                                                   [:> Text {:as "p"}
                                                    "This action will instantly remove your access to "
                                                    [:strong (:name resource)]
                                                    " and all roles associated with it. This action can not be undone."]
                                                   [:> Text {:as "p"}
                                                    "Are you sure you want to delete this resource?"]]
                                            :on-success (fn []
                                                          (rf/dispatch [:resources->delete-resource (:name resource)]))}]))]

          (when (and (not loading?) resource (nil? @new-name))
            (reset! new-name (:name resource)))

        [page-wrapper/main
         {:children
          [:> Box {:class "h-[calc(100vh-65px)] bg-[--gray-1] px-4 py-10 sm:px-6 lg:px-20 lg:pt-6 lg:pb-10"}
           (if (or loading? (nil? resource))
             [loading-view]
             [:> Box
              [header resource]

              ;; Tabs
              [:> Tabs.Root {:value @active-tab
                             :onValueChange #(reset! active-tab %)}
               [:> Tabs.List {:size "2"}
                [:> Tabs.Trigger {:value "information"}
                 "Resource Information"]
                [:> Tabs.Trigger {:value "roles"}
                 "Resource Roles"]]

               [:> Box {:class "mt-10"}
                [:> Tabs.Content {:value "information"}
                 [information-tab/main resource new-name]]

                [:> Tabs.Content {:value "roles"}
                 [roles-tab/main resource-id]]]]])]

          :footer-props
          {:form-type :update
           :back-text "Back"
           :next-text "Save"
           :on-back #(js/history.back)
           :on-next handle-save
           :next-disabled? (or @updating?
                               (not resource)
                               (cs/blank? @new-name)
                               (= @new-name (:name resource)))
           :on-delete handle-delete}}]))))

