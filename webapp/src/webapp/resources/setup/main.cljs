(ns webapp.resources.setup.main
  (:require
   ["@radix-ui/themes" :refer [Box Flex]]
   [re-frame.core :as rf]
   [webapp.resources.setup.page-wrapper :as page-wrapper]
   [webapp.resources.setup.resource-name-step :as resource-name-step]
   [webapp.resources.setup.agent-step :as agent-step]
   [webapp.resources.setup.roles-step :as roles-step]
   [webapp.resources.setup.success-step :as success-step]
   [webapp.resources.helpers :as helpers]))

(defn main []
  (let [current-step @(rf/subscribe [:resource-setup/current-step])
        resource-name @(rf/subscribe [:resource-setup/resource-name])
        agent-id @(rf/subscribe [:resource-setup/agent-id])
        roles @(rf/subscribe [:resource-setup/roles])
        creating? @(rf/subscribe [:resources->creating?])
        onboarding? (helpers/is-onboarding-context?)
        connections-metadata @(rf/subscribe [:connections->metadata])]

    ;; Load metadata and agents on mount if not already loaded
    (when (nil? connections-metadata)
      (rf/dispatch [:connections->load-metadata]))

    [page-wrapper/main
     {:onboarding? onboarding?
      :children
      [:> Box {:class "h-full"}
       ;; Show Hoop logo only in onboarding mode and NOT in success step
       (when (and onboarding? (not= current-step :success))
         [:> Flex {:justify "start" :class "px-8"}
          [:figure
           [:img {:src "/images/hoop-branding/PNG/hoop-symbol_black@4x.png"
                  :alt "Hoop Logo"
                  :class "w-16"}]]])

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
       :on-cancel (fn []
                    (if (= current-step :resource-name)
                      (.back js/history)
                      (rf/dispatch [:resource-setup->back])))}}]))

