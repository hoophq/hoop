(ns webapp.connections.views.setup.network
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text Heading Switch]]

   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.configuration-inputs :as configuration-inputs]
   [webapp.connections.views.setup.connection-method :as connection-method]))

(defn http-credentials-form []
  (let [credentials @(rf/subscribe [:connection-setup/network-credentials])
        connection-method @(rf/subscribe [:connection-setup/connection-method])
        show-selector? (= connection-method "secrets-manager")
        remote-url-value (if (map? (:remote_url credentials))
                           (:value (:remote_url credentials))
                           (or (:remote_url credentials) ""))
        insecure-value (let [raw-insecure (:insecure credentials)]
                         (cond
                           (boolean? raw-insecure) raw-insecure
                           (map? raw-insecure) (let [value-str (:value raw-insecure)]
                                                 (if (string? value-str)
                                                   (= value-str "true")
                                                   (boolean value-str)))
                           (string? raw-insecure) (= raw-insecure "true")
                           :else false))]
    [:form
     {:id "credentials-form"
      :on-submit (fn [e]
                   (.preventDefault e)
                   (rf/dispatch [:connection-setup/next-step :additional-config]))}
     [:> Box {:class "max-w-[600px]"}
      [:> Box {:class "space-y-7"}
       [connection-method/main "httpproxy"]

       [:> Box {:class "space-y-4"}
        [:> Text {:size "4" :weight "bold"} "Environment credentials"]

        ;; Remote URL input
        [forms/input
         {:label "Remote URL"
          :placeholder "e.g. https://example.com"
          :required true
          :value remote-url-value
          :on-change #(rf/dispatch [:connection-setup/update-network-credentials
                                    "remote_url"
                                    (-> % .-target .-value)])
          :start-adornment (when show-selector?
                             [connection-method/source-selector "remote_url"])}]]

       ;; HTTP Headers Section
       [configuration-inputs/http-headers-section]

       ;; Allow insecure SSL switch
       [:> Flex {:align "center" :gap "3"}
        [:> Switch {:checked insecure-value
                    :size "3"
                    :onCheckedChange #(rf/dispatch [:connection-setup/toggle-network-insecure (boolean %)])}]
        [:> Box
         [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"}
          "Allow insecure SSL"]
         [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
          "Skip SSL certificate verification for HTTPS connections."]]]

       [agent-selector/main]]]]))

(defn tcp-credentials-form []
  (let [credentials @(rf/subscribe [:connection-setup/network-credentials])
        connection-method @(rf/subscribe [:connection-setup/connection-method])
        show-selector? (= connection-method "secrets-manager")
        host-value (if (map? (:host credentials))
                     (:value (:host credentials))
                     (or (:host credentials) ""))
        port-value (if (map? (:port credentials))
                     (:value (:port credentials))
                     (or (:port credentials) ""))]
    [:form
     {:id "credentials-form"
      :on-submit (fn [e]
                   (.preventDefault e)
                   (rf/dispatch [:connection-setup/next-step :additional-config]))}
     [:> Box {:class "max-w-[600px]"}
      [:> Box {:class "space-y-5"}
       [connection-method/main "tcp"]

       [:> Text {:size "4" :weight "bold"} "Environment credentials"]

       ;; Host input
       [forms/input
        {:label "Host"
         :placeholder "e.g. localhost"
         :required true
         :type "text"
         :value host-value
         :on-change #(rf/dispatch [:connection-setup/update-network-credentials
                                   "host"
                                   (-> % .-target .-value)])
         :start-adornment (when show-selector?
                            [connection-method/source-selector "host"])}]

       ;; Port input
       [forms/input
        {:label "Port"
         :placeholder "e.g. 4040"
         :required true
         :type "text"
         :value port-value
         :on-change #(rf/dispatch [:connection-setup/update-network-credentials
                                   "port"
                                   (-> % .-target .-value)])
         :start-adornment (when show-selector?
                            [connection-method/source-selector "port"])}]

       [agent-selector/main]]]]))

(defn credentials-form []
  (let [selected-subtype @(rf/subscribe [:connection-setup/connection-subtype])]
    (case selected-subtype
      "tcp" [tcp-credentials-form]
      "httpproxy" [http-credentials-form]
      "grafana" [http-credentials-form]
      "kibana" [http-credentials-form]
      nil)))

