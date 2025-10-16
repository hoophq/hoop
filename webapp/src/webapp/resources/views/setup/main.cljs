(ns webapp.resources.views.setup.main
  (:require
   ["@radix-ui/themes" :refer [Box]]
   [re-frame.core :as rf]
   [webapp.resources.views.setup.page-wrapper :as page-wrapper]
   [webapp.resources.views.setup.resource-name-step :as resource-name-step]
   [webapp.resources.views.setup.agent-step :as agent-step]
   [webapp.resources.views.setup.roles-step :as roles-step]
   [webapp.resources.views.setup.success-step :as success-step]))

(defn main []
  (let [current-step @(rf/subscribe [:resource-setup/current-step])
        resource-name @(rf/subscribe [:resource-setup/resource-name])
        agent-id @(rf/subscribe [:resource-setup/agent-id])
        roles @(rf/subscribe [:resource-setup/roles])
        creating? @(rf/subscribe [:resources->creating?])
        _ (js/console.log "📍 Current step:" current-step "agent-id:" agent-id)]

    [page-wrapper/main
     {:children
      [:> Box {:class "min-h-screen bg-gray-1"}
       (case current-step
         :resource-name [resource-name-step/main]
         :agent-selector [agent-step/main]
         :roles [roles-step/main]
         :success [success-step/main]
         [resource-name-step/main])]

      :footer-props
      {:hide-footer? (= current-step :success)
       :back-hidden? (= current-step :resource-name)
       :next-text (case current-step
                    :resource-name "Next: Agents"
                    :agent-selector "Next: Resource Roles"
                    :roles (if creating? "Creating..." "Save and Finish")
                    "Next")
       :next-disabled? (case current-step
                         :resource-name (empty? resource-name)
                         :agent-selector (nil? agent-id)
                         :roles (or (empty? roles) creating?)
                         false)
       :on-next (fn []
                  (let [form-id (case current-step
                                  :resource-name "resource-name-form"
                                  :roles "roles-form"
                                  nil)]
                    (when form-id
                      (when-let [form (.getElementById js/document form-id)]
                        (when (.reportValidity form)
                          (let [event (js/Event. "submit" #js {:bubbles true :cancelable true})]
                            (.dispatchEvent form event))))))

                  ;; Agent selector doesn't have a form, just navigate
                  (when (= current-step :agent-selector)
                    (when agent-id
                      (rf/dispatch [:resource-setup->next-step :roles]))))
       :on-cancel #(rf/dispatch [:navigate :resource-catalog])}}]))

