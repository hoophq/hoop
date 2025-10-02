(ns webapp.connections.views.setup.network
  (:require
   ["@radix-ui/themes" :refer [Box Flex Grid RadioGroup Text Heading Switch]]
   ["lucide-react" :refer [Network]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.connections.views.setup.additional-configuration :as additional-configuration]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.configuration-inputs :as configuration-inputs]
   [webapp.connections.views.setup.headers :as headers]
   [webapp.connections.views.setup.page-wrapper :as page-wrapper]))

(def network-types
  [{:id "tcp" :title "TCP"}
   {:id "httpproxy" :title "HTTP Proxy"}])

(defn http-credentials-form []
  (let [credentials @(rf/subscribe [:connection-setup/network-credentials])]
    [:form
     {:id "credentials-form"
      :on-submit (fn [e]
                   (.preventDefault e)
                   (rf/dispatch [:connection-setup/next-step :additional-config]))}
     [:> Box {:class "max-w-[600px]"}
      [:> Box {:class "space-y-7"}
       [:> Box {:class "space-y-4"}
        [:> Text {:size "4" :weight "bold"} "Environment credentials"]

        ;; Remote URL input
        [forms/input
         {:label "Remote URL"
          :placeholder "e.g. https://example.com"
          :required true
          :value (get credentials :remote_url "")
          :on-change #(rf/dispatch [:connection-setup/update-network-remote-url
                                    (-> % .-target .-value)])}]]

       ;; HTTP Headers Section
       [configuration-inputs/environment-variables-section
        {:title "HTTP headers"
         :subtitle "Add HTTP headers that will be used in your requests."
         :hide-default-title true}]

       ;; Allow insecure SSL switch
       [:> Flex {:align "center" :gap "3"}
        [:> Switch {:checked (get credentials :insecure false)
                    :size "3"
                    :onCheckedChange #(rf/dispatch [:connection-setup/toggle-network-insecure %])}]
        [:> Box
         [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"}
          "Allow insecure SSL"]
         [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
          "Skip SSL certificate verification for HTTPS connections."]]]

       [agent-selector/main]]]]))

(defn tcp-credentials-form []
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
         :type "password"
         :value (get credentials :host "")
         :on-change #(rf/dispatch [:connection-setup/update-network-host
                                   (-> % .-target .-value)])}]

       ;; Port input
       [forms/input
        {:label "Port"
         :placeholder "e.g. 4040"
         :required true
         :type "password"
         :value (get credentials :port "")
         :on-change #(rf/dispatch [:connection-setup/update-network-port
                                   (-> % .-target .-value)])}]

       [agent-selector/main]]]]))

(defn credentials-form []
  (let [selected-subtype @(rf/subscribe [:connection-setup/connection-subtype])]
    (case selected-subtype
      "tcp" [tcp-credentials-form]
      "httpproxy" [http-credentials-form]
      nil)))

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

     (when selected-subtype
       [credentials-form])]))

(defn main [form-type]
  (let [network-type @(rf/subscribe [:connection-setup/network-type])
        current-step @(rf/subscribe [:connection-setup/current-step])
        credentials @(rf/subscribe [:connection-setup/network-credentials])
        agent-id @(rf/subscribe [:connection-setup/agent-id])]

    [page-wrapper/main
     {:children [:> Box {:class "max-w-[600px] mx-auto p-6 space-y-7"}
                 [headers/setup-header form-type]

                 (case current-step
                   :resource [resource-step]
                   :additional-config [additional-configuration/main
                                       {:selected-type network-type
                                        :form-type form-type
                                        :submit-fn #(rf/dispatch [:connection-setup/submit])}]
                   [resource-step])]

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
                                                        (empty? (get credentials :port))))
                                                  (and (= network-type "httpproxy")
                                                       (empty? (get credentials :remote_url))))
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
