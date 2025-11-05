(ns webapp.resources.views.add-role.main
  (:require
   ["@radix-ui/themes" :refer [Box]]
   [re-frame.core :as rf]
   [webapp.components.loaders :as loaders]
   [webapp.resources.views.setup.page-wrapper :as page-wrapper]
   [webapp.resources.views.setup.roles-step :as roles-step]
   [webapp.resources.views.setup.success-step :as success-step]
   [webapp.resources.views.add-role.events]))

(defn loading-view []
  [:div {:class "flex items-center justify-center bg-gray-1 h-full"}
   [loaders/simple-loader]])

(defn main [resource-id]
  (let [current-step (rf/subscribe [:resource-setup/current-step])
        loading? (rf/subscribe [:resource-setup/loading?])
        submitting? (rf/subscribe [:resource-setup/submitting?])
        roles (rf/subscribe [:resource-setup/roles])]

    (rf/dispatch [:add-role->initialize resource-id])

    (fn []
      (if @loading?
        [loading-view]

        [page-wrapper/main
         {:onboarding? false
          :children
          [:> Box {:class "h-full"}
           (case @current-step
             :roles [roles-step/main]
             :success [success-step/main]
             [loading-view])]

          :footer-props
          {:hide-footer? (= @current-step :success)
           :back-hidden? true
           :next-text (if @submitting? "Creating..." "Save and Finish")
           :next-disabled? (or (empty? @roles) @submitting?)
           :on-next (fn []
                      (when (= @current-step :roles)
                        (let [form (.getElementById js/document "roles-form")]
                          (when form
                            (when (.reportValidity form)
                              (let [event (js/Event. "submit" #js {:bubbles true :cancelable true})]
                                (.dispatchEvent form event)))))))
           :on-cancel #(rf/dispatch [:navigate :configure-resource {} :resource-id resource-id])}}]))))

