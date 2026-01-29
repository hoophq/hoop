;; server.cljs
(ns webapp.connections.views.setup.server
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Badge Card Flex Grid Heading RadioGroup Text Switch]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]
   [webapp.connections.constants :refer [connection-configs-required]]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.configuration-inputs :as configuration-inputs]
   [webapp.connections.views.setup.connection-method :as connection-method]))

(defn resource-subtype-override-section []
  (let [resource-subtype-override @(rf/subscribe [:connection-setup/resource-subtype-override])]
    [:> Box {:class "space-y-4"}
     [:> Flex {:align "center" :gap "2"}
      [:> Heading {:size "3"} "Resource Subtype Override"]
      [:> Badge {:variant "solid" :color "green" :size "1"} "Beta"]]

     [:> Text {:size "2" :color "gray"}
      "Configure your resource role for specific resource types. Select a subtype only if it matches your actual resource, applying the optimal settings for that resource type. "]
     [:> Text {:size "2" :color "gray"}
      "This feature is currently in Beta to streamline resource roles to most common resource types."]

     [:> Box
      [forms/select
       {:options [{:text "DynamoDB" :value "dynamodb"}
                  {:text "CloudWatch" :value "cloudwatch"}]
        :selected (or resource-subtype-override "")
        :placeholder "Select one"
        :on-change #(rf/dispatch [:connection-setup/set-resource-subtype-override %])
        :full-width? true
        :not-margin-bottom? true}]]]))

(defn credentials-step [& [mode]]
  [:form
   {:id "credentials-form"
    :on-submit (fn [e]
                 (.preventDefault e)
                 (rf/dispatch [:connection-setup/next-step :additional-config]))}
   [:> Box {:class "space-y-8 max-w-[600px]"}
    [connection-method/main "custom"]

    ;; Environment Variables Section
    [configuration-inputs/environment-variables-section]

    ;; Configuration Files Section
    [configuration-inputs/configuration-files-section]

    ;; Additional Command Section
    [:> Box {:class "space-y-4"}
     [:> Heading {:size "3"} "Additional command"]
     [:> Text {:size "2" :color "gray"}
      "Each argument should be entered separately."
      [:br]
      "Press Enter after each argument to add it to the list."]
     [:> Box
      [multi-select/text-input
       {:value @(rf/subscribe [:connection-setup/command-args])
        :input-value @(rf/subscribe [:connection-setup/command-current-arg])
        :on-change #(rf/dispatch [:connection-setup/set-command-args %])
        :on-input-change #(rf/dispatch [:connection-setup/set-command-current-arg %])
        :label "Command Arguments"
        :id "command-args"
        :name "command-args"}]
      [:> Text {:size "2" :color "gray" :mt "2"}
       "Example: 'python', '-m', 'http.server', '8000'"]]]

    ;; Resource Subtype Override Section (only in update mode)
    (when (= mode :update)
      [resource-subtype-override-section])

    ;; Agent Section
    [agent-selector/main]]])

(defn render-ssh-field [{:keys [key label value required hidden placeholder type]}]
  (let [connection-method @(rf/subscribe [:connection-setup/connection-method])
        show-source-selector? (= connection-method "secrets-manager")
        field-value (if (map? value) (:value value) (str value))
        handle-change (fn [e]
                        (let [new-value (-> e .-target .-value)]
                          (rf/dispatch [:connection-setup/update-ssh-credentials
                                        key
                                        new-value])))
        base-props {:label label
                    :placeholder (or placeholder (str "e.g. " key))
                    :value field-value
                    :required required
                    :type (if (= key "pass") "password" "text")
                    :hidden hidden
                    :on-change handle-change}]
    (cond
      (= type "textarea")
      [forms/textarea base-props]

      :else
      [forms/input (assoc base-props
                          :start-adornment (when show-source-selector?
                                             [connection-method/source-selector key]))])))

;; Registrar um evento para controlar o método de autenticação
(rf/reg-event-db
 :connection-setup/set-ssh-auth-method
 (fn [db [_ method]]
   (assoc-in db [:connection-setup :ssh-auth-method] method)))

;; Registrar um subscription para acessar o método de autenticação
(rf/reg-sub
 :connection-setup/ssh-auth-method
 (fn [db]
   (get-in db [:connection-setup :ssh-auth-method] "password"))) ;; "password" ou "key"

(defn ssh-credentials []
  (let [configs (get connection-configs-required :ssh)
        credentials @(rf/subscribe [:connection-setup/ssh-credentials])
        auth-method @(rf/subscribe [:connection-setup/ssh-auth-method])
        filtered-fields (filter (fn [field]
                                  (case auth-method
                                    "password" (not= (:key field) "authorized_server_keys")
                                    "key" (not= (:key field) "pass")
                                    true))
                                configs)]
    [:form
     {:id "ssh-credentials-form"
      :on-submit (fn [e]
                   (.preventDefault e)
                   (rf/dispatch [:connection-setup/next-step :additional-config]))}
     [:> Box {:class "space-y-8 max-w-[600px]"}
      [:> Box {:class "space-y-4"}
       [:> Box
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "SSH Configuration"]
        [:> Text {:as "p" :size "3" :class "text-[--gray-11]" :mb "5"}
         "Provide SSH information to setup your connection."]]

       [connection-method/main "ssh"]

       [:> Box {:class "space-y-4 mb-6"}
        [:> Heading {:as "h4" :size "3" :weight "medium"}
         "Authentication Method"]
        [:> RadioGroup.Root
         {:value auth-method
          :on-value-change #(rf/dispatch [:connection-setup/set-ssh-auth-method %])}
         [:> Flex {:direction "column" :gap "2"}
          [:> RadioGroup.Item {:value "password"} "Username & Password"]
          [:> RadioGroup.Item {:value "key"} "Private Key Authentication"]]]]

       [:> Grid {:columns "1" :gap "4"}
        (for [field filtered-fields]
          ^{:key (:key field)}
          [render-ssh-field (assoc field
                                   :value (get credentials (:key field) (:value field)))])

        [agent-selector/main]]]]]))

(defn kubernetes-token []
  (let [credentials @(rf/subscribe [:connection-setup/kubernetes-token])
        connection-method @(rf/subscribe [:connection-setup/connection-method])
        show-selector? (= connection-method "secrets-manager")
        cluster-url-value (if (map? (:cluster_url credentials))
                            (:value (:cluster_url credentials))
                            (or (:cluster_url credentials) ""))
        auth-token-value (if (map? (:authorization credentials))
                           (:value (:authorization credentials))
                           (or (:authorization credentials) ""))
        auth-token-display-value (if (cs/starts-with? auth-token-value "Bearer ")
                                   (subs auth-token-value 7)
                                   auth-token-value)
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
     {:id "kubernetes-token-form"
      :on-submit (fn [e]
                   (.preventDefault e)
                   (rf/dispatch [:connection-setup/next-step :additional-config]))}
     [:> Box {:class "space-y-8 max-w-[600px]"}
      [:> Box {:class "space-y-4"}
       [connection-method/main "kubernetes-token"]

       ;; Cluster URL
       [forms/input {:label "Cluster URL"
                     :placeholder "e.g. https://kubernetes.default.svc.cluster.local:443"
                     :value cluster-url-value
                     :required true
                     :type "text"
                     :on-change (fn [e]
                                  (let [new-value (-> e .-target .-value)]
                                    (rf/dispatch [:connection-setup/set-kubernetes-token
                                                  "cluster_url"
                                                  new-value])))
                     :start-adornment (when show-selector?
                                        [connection-method/source-selector "cluster_url"])}]

       [forms/input {:label "Authorization token"
                     :placeholder "e.g. jwt.token.example"
                     :value auth-token-display-value
                     :required true
                     :type "text"
                     :on-change (fn [e]
                                  (let [new-value (-> e .-target .-value)]
                                    (rf/dispatch [:connection-setup/set-kubernetes-token
                                                  "authorization"
                                                  new-value])))
                     :start-adornment (when show-selector?
                                        [connection-method/source-selector "authorization"])}]

       [:> Flex {:align "center" :gap "3"}
        [:> Switch {:checked insecure-value
                    :size "3"
                    :onCheckedChange #(rf/dispatch [:connection-setup/set-kubernetes-token
                                                    "insecure"
                                                    (boolean %)])}]
        [:> Box
         [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"}
          "Allow insecure SSL"]
         [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
          "Skip SSL certificate verification for HTTPS connections."]]]
       [agent-selector/main]]]]))
