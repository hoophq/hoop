(ns webapp.connections.views.setup.network
  (:require
   ["@radix-ui/themes" :refer [Box Flex Grid RadioGroup Text]]
   ["lucide-react" :refer [Network]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.connections.views.setup.additional-configuration :as additional-configuration]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.headers :as headers]
   [webapp.connections.views.setup.page-wrapper :as page-wrapper]))

(def network-types
  [{:id "tcp" :title "TCP"}
   {:id "http" :title "HTTP (soon)" :disabled true}])

(defn credentials-form []
  (let [credentials @(rf/subscribe [:connection-setup/network-credentials])]
    [:form
     {:id "credentials-form"
      :on-submit (fn [e]
                   (.preventDefault e)
                   (rf/dispatch [:connection-setup/next-step :additional-config]))}
     [:> Box {:class "max-w-[600px]"}
      [:> Box {:class "space-y-5"}
       [:> Text {:size "4" :weight "bold"} "Environment credentials"]

     ;; Host input
       [forms/input
        {:label "Host"
         :placeholder "e.g. localhost"
         :required true
         :value (get credentials :host "")
         :on-change #(rf/dispatch [:connection-setup/update-network-host
                                   (-> % .-target .-value)])}]

     ;; Port input
       [forms/input
        {:label "Port"
         :placeholder "e.g. username"
         :required true
         :value (get credentials :port "")
         :on-change #(rf/dispatch [:connection-setup/update-network-port
                                   (-> % .-target .-value)])}]

       [agent-selector/main]]]]))

(defn resource-step []
  (let [selected-subtype @(rf/subscribe [:connection-setup/connection-subtype])]
    [:> Box {:class "space-y-5"}
     [:> Text {:size "4" :weight "bold"} "Network access type"]
     [:> RadioGroup.Root {:name "network-type"
                          :value selected-subtype
                          :on-value-change #(rf/dispatch [:connection-setup/select-connection "network" %])}
      [:> Grid {:columns "1" :gap "3"}
       (for [{:keys [id title disabled]} network-types]
         ^{:key id}
         [:> RadioGroup.Item
          {:value id
           :class (str "p-4 " (when disabled "opacity-50 cursor-not-allowed"))
           :disabled disabled}
          [:> Flex {:gap "3" :align "center"}
           [:> Network {:size 16}]
           title]])]]

     (when (= selected-subtype "tcp")
       [:<>
        [credentials-form]
        ;; Environment Variables Section
        #_[configuration-inputs/environment-variables-section]])]))

(defn main [form-type]
  (let [network-type @(rf/subscribe [:connection-setup/network-type])
        current-step @(rf/subscribe [:connection-setup/current-step])
        credentials @(rf/subscribe [:connection-setup/network-credentials])
        agent-id @(rf/subscribe [:connection-setup/agent-id])]

    [page-wrapper/main
     {:children [:> Box {:class "max-w-[600px] mx-auto p-6 space-y-7"}
                 [headers/setup-header]

                 (case current-step
                   :resource [resource-step]
                   :additional-config [additional-configuration/main
                                       {:selected-type network-type
                                        :submit-fn #(rf/dispatch [:connection-setup/submit])}]
                   [resource-step])]

                     ;; Footer
      :footer-props {:form-type form-type
                     :next-text (if (= current-step :additional-config)
                                  "Confirm"
                                  "Next: Configuration")
                     :next-disabled? (case current-step
                                       :resource (or
                                                  (not network-type)
                                                  (and (= network-type "tcp")
                                                       (or
                                                        (empty? (get credentials :host))
                                                        (empty? (get credentials :port)))))
                                       false)
                     :on-next (fn []
                                (let [form (.getElementById js/document
                                                            (if (= current-step :credentials)
                                                              "credentials-form"
                                                              "additional-config-form"))]
                                  (when form
                                    (if (and (.reportValidity form)
                                             agent-id)
                                      (let [event (js/Event. "submit" #js {:bubbles true :cancelable true})]
                                        (.dispatchEvent form event))
                                      (js/console.warn "Invalid form!")))))}}]))
