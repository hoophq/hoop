(ns webapp.resources.setup.roles-step
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Link Separator Text Switch]]
   ["lucide-react" :refer [ArrowUpRight Plus Trash2]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]
   [webapp.resources.constants :as constants]
   [webapp.resources.setup.configuration-inputs :as configuration-inputs]
   [webapp.resources.setup.connection-method :as connection-method]))


;; SSH role form - Based on server.cljs
(defn ssh-role-form [role-index]
  (let [configs (constants/get-role-config "application" "ssh")
        credentials @(rf/subscribe [:resource-setup/role-credentials role-index])
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])
        ;; Local state for auth method (default to password)
        auth-method (or (get credentials "auth-method") "password")
        filtered-fields (filter (fn [field]
                                  (case auth-method
                                    "password" (not= (:key field) "authorized_server_keys")
                                    "key" (not= (:key field) "pass")
                                    true))
                                configs)]
    [:> Box {:class "space-y-4"}
     ;; Authentication Method Selector
     [:> Box {:class "space-y-4 mb-6"}
      [:> Heading {:as "h4" :size "3" :weight "medium"}
       "Authentication Method"]
      [:> Grid {:columns "2" :gap "3"}
       [:> Button {:size "2"
                   :type "button"
                   :variant (if (= auth-method "password") "solid" "outline")
                   :on-click #(rf/dispatch [:resource-setup->update-role-credentials
                                            role-index
                                            "auth-method"
                                            "password"])}
        "Username & Password"]
       [:> Button {:size "2"
                   :type "button"
                   :variant (if (= auth-method "key") "solid" "outline")
                   :on-click #(rf/dispatch [:resource-setup->update-role-credentials
                                            role-index
                                            "auth-method"
                                            "key"])}
        "Private Key Authentication"]]]

     ;; SSH Fields (filtered based on auth method)
     [:> Grid {:columns "1" :gap "4"}
      (for [field filtered-fields]
        ^{:key (:key field)}
        (let [field-key (:key field)
              field-value (get credentials field-key "")
              show-source-selector? (= connection-method "secrets-manager")
              display-value field-value
              handle-change (fn [e]
                              (let [new-value (-> e .-target .-value)]
                                (rf/dispatch [:resource-setup->update-role-credentials
                                              role-index
                                              field-key
                                              new-value])))
              base-props {:label (:label field)
                          :placeholder (or (:placeholder field) (str "e.g. " field-key))
                          :value display-value
                          :required (:required field)
                          :type "password"
                          :on-change handle-change
                          :start-adornment (when show-source-selector?
                                             [connection-method/source-selector role-index field-key])}]
          (if (= (:type field) "textarea")
            [forms/textarea (dissoc base-props :type :start-adornment)]
            [forms/input base-props])))]]))

;; TCP role form - Based on network.cljs
(defn tcp-role-form [role-index]
  (let [configs (constants/get-role-config "application" "tcp")
        credentials @(rf/subscribe [:resource-setup/role-credentials role-index])
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])]
    [:> Grid {:columns "1" :gap "4"}
     (for [field configs]
       ^{:key (:key field)}
       (let [field-key (:key field)
             field-value (get credentials field-key "")
             show-source-selector? (= connection-method "secrets-manager")
             display-value field-value
             handle-change (fn [e]
                             (let [new-value (-> e .-target .-value)]
                               (rf/dispatch [:resource-setup->update-role-credentials
                                             role-index
                                             field-key
                                             new-value])))]
         [forms/input {:label (:label field)
                       :placeholder (or (:placeholder field) (str "e.g. " field-key))
                       :value display-value
                       :required (:required field)
                       :type "password"
                       :on-change handle-change
                       :start-adornment (when show-source-selector?
                                          [connection-method/source-selector role-index field-key])}]))]))

;; Kubernetes Token role form - Based on network.cljs
(defn kubernetes-token-role-form [role-index]
  (let [credentials @(rf/subscribe [:resource-setup/role-credentials role-index])
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])
        show-selector? (= connection-method "secrets-manager")
        remote-url-value (get credentials "remote_url" "")
        auth-token-value (get credentials "header_Authorization" "")
        auth-token-display-value (if (cs/starts-with? auth-token-value "Bearer ")
                                   (subs auth-token-value 7)
                                   auth-token-value)]
    (when (nil? (get credentials "insecure"))
      (rf/dispatch [:resource-setup->update-role-credentials
                    role-index
                    "insecure"
                    false]))

    [:> Box {:class "space-y-4"}
     ;; Cluster URL
     [forms/input {:label "Cluster URL"
                   :placeholder "e.g. https://kubernetes.default.svc.cluster.local:443"
                   :value remote-url-value
                   :required true
                   :type "text"
                   :on-change (fn [e]
                                (let [new-value (-> e .-target .-value)]
                                  (rf/dispatch [:resource-setup->update-role-credentials
                                                role-index
                                                "remote_url"
                                                new-value])))
                   :start-adornment (when show-selector?
                                      [connection-method/source-selector role-index "remote_url"])}]

     [forms/input {:label "Authorization token"
                   :placeholder "e.g. jwt.token.example"
                   :value auth-token-display-value
                   :required true
                   :type "text"
                   :on-change (fn [e]
                                (let [new-value (-> e .-target .-value)
                                      transformed-val (str "Bearer " new-value)]
                                  (rf/dispatch [:resource-setup->update-role-credentials
                                                role-index
                                                "header_Authorization"
                                                transformed-val])))
                   :start-adornment (when show-selector?
                                      [connection-method/source-selector role-index "header_Authorization"])}]

     [:> Flex {:align "center" :gap "3"}
      [:> Switch {:checked (get credentials "insecure" false)
                  :size "3"
                  :onCheckedChange #(rf/dispatch [:resource-setup->update-role-credentials
                                                  role-index
                                                  "insecure"
                                                  %])}]
      [:> Box
       [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"}
        "Allow insecure SSL"]
       [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
        "Skip SSL certificate verification for HTTPS connections."]]]]))

(defn http-proxy-role-form [role-index]
  (let [credentials @(rf/subscribe [:resource-setup/role-credentials role-index])
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])
        remote-url-value (get credentials "remote_url" "")
        show-selector? (= connection-method "secrets-manager")
        handle-remote-url-change (fn [e]
                                   (let [new-value (-> e .-target .-value)]
                                     (rf/dispatch [:resource-setup->update-role-credentials
                                                   role-index
                                                   "remote_url"
                                                   new-value])))]
    (when (nil? (get credentials "insecure"))
      (rf/dispatch [:resource-setup->update-role-credentials
                    role-index
                    "insecure"
                    false]))
      [:> Box {:class "space-y-4"}
       ;; Remote URL
       [forms/input {:label "Remote URL"
                     :placeholder "e.g. http://example.com"
                     :value remote-url-value
                     :required true
                     :type "text"
                     :on-change handle-remote-url-change
                     :start-adornment (when show-selector?
                                        [connection-method/source-selector role-index "remote_url"])}]

       ;; HTTP headers section (usando configuration-inputs)
       [configuration-inputs/environment-variables-section role-index
        {:title "HTTP headers"
         :subtitle "Add HTTP headers that will be used in your requests."}]

       [:> Flex {:align "center" :gap "3"}
        [:> Switch {:checked (get credentials "insecure" false)
                    :size "3"
                    :onCheckedChange #(rf/dispatch [:resource-setup->update-role-credentials
                                                    role-index
                                                    "insecure"
                                                    %])}]
        [:> Box
         [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"}
          "Allow insecure SSL"]
         [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
          "Skip SSL certificate verification for HTTPS connections."]]]]))


;; Custom/Metadata-driven role form (includes databases)
(defn metadata-driven-role-form [role-index]
  (let [connection @(rf/subscribe [:resource-setup/current-connection-metadata])
        credentials-config (get-in connection [:resourceConfiguration :credentials])
        metadata-credentials @(rf/subscribe [:resource-setup/metadata-credentials role-index])
        config-files @(rf/subscribe [:resource-setup/role-config-files role-index])
        config-files-map (into {} (map (fn [{:keys [key value]}] [key value]) config-files))
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])
        is-aws-iam-role? (= connection-method "aws-iam-role")
        is-secrets-manager? (= connection-method "secrets-manager")]
    (when (seq credentials-config)
      [:> Box
       [:> Grid {:columns "1" :gap "4"}
        (for [field credentials-config]
          (let [sanitized-name (cs/capitalize
                                (cs/lower-case
                                 (cs/replace (:name field) #"[^a-zA-Z0-9]" " ")))
                env-var-name (:name field)
                field-type (:type field)
                is-filesystem? (= field-type "filesystem")
                is-password? (= env-var-name "PASS")
                should-hide? (and is-aws-iam-role? is-password?)]

            (when-not should-hide?
              (let [field-value (if is-filesystem?
                                  (get config-files-map env-var-name "")
                                  (get metadata-credentials env-var-name ""))
                    display-type (case field-type
                                   "filesystem" "textarea"
                                   "textarea" "textarea"
                                   "password")
                    show-source-selector? is-secrets-manager?
                    display-value field-value
                    handle-change (fn [e]
                                    (let [new-value (-> e .-target .-value)
                                          field-source @(rf/subscribe [:resource-setup/field-source role-index env-var-name])]
                                      (if is-filesystem?
                                        (rf/dispatch [:resource-setup->update-role-config-file-by-key
                                                      role-index
                                                      env-var-name
                                                      new-value])
                                        (rf/dispatch [:resource-setup->update-role-metadata-credentials
                                                      role-index
                                                      env-var-name
                                                      new-value
                                                      field-source]))))]
                ^{:key env-var-name}
                (if (= display-type "textarea")
                  [forms/textarea {:label sanitized-name
                                   :placeholder (or (:placeholder field) (:description field))
                                   :value display-value
                                   :required (:required field)
                                   :helper-text (:description field)
                                   :on-change handle-change}]
                  [forms/input {:label sanitized-name
                                :placeholder (or (:placeholder field) (:description field))
                                :value display-value
                                :required (:required field)
                                :type display-type
                                :helper-text (:description field)
                                :on-change handle-change
                                :start-adornment (when show-source-selector?
                                                   [connection-method/source-selector role-index env-var-name])}])))))]])))

;; Linux/Container role form - Based on server.cljs
(defn linux-container-role-form [role-index]
  [:> Box {:class "space-y-6"}
   ;; Environment variables section
   [configuration-inputs/environment-variables-section role-index {}]

   ;; Configuration files section
   [configuration-inputs/configuration-files-section role-index]

   ;; Additional command section
   [:> Box {:class "space-y-4"}
    [:> Heading {:as "h4" :size "3" :weight "medium"}
     "Additional command"]
    [:> Text {:size "2" :color "gray"}
     "Each argument should be entered separately."
     [:br]
     "Press Enter after each argument to add it to the list."]
    [:> Box
     [multi-select/text-input
      {:value (clj->js @(rf/subscribe [:resource-setup/role-command-args role-index]))
       :input-value @(rf/subscribe [:resource-setup/role-command-current-arg role-index])
       :on-change #(rf/dispatch [:resource-setup->set-role-command-args role-index %])
       :on-input-change #(rf/dispatch [:resource-setup->set-role-command-current-arg role-index %])
       :label "Command Arguments"
       :id "command-args"
       :name "command-args"}]
     [:> Text {:size "2" :color "gray" :mt "2"}
      "Example: 'python', '-m', 'http.server', '8000'"]]]])

(defn role-configuration [role-index]
  (let [roles @(rf/subscribe [:resource-setup/roles])
        role (get roles role-index)
        resource-subtype @(rf/subscribe [:resource-setup/resource-subtype])
        connection @(rf/subscribe [:resource-setup/current-connection-metadata])
        credentials-config (get-in connection [:resourceConfiguration :credentials])
        has-env-vars? (contains? #{"linux-vm" "httpproxy"} resource-subtype)
        has-credentials? (seq credentials-config)
        should-show-connection-method? (or has-credentials?
                                           has-env-vars?)
        can-remove? (> (count roles) 1)]

    [:> Grid {:columns "7" :gap "7"}
     ;; Left side - "New Role" description
     [:> Box {:grid-column "span 3 / span 3"}
      [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
       "New Role"]
      [:> Text {:size "2" :class "text-[--gray-11]"}
       "Fill out the information to access your Resource with this specific Role."]]

     ;; Right side - Form fields
     [:> Box {:grid-column "span 4 / span 4" :class "space-y-8"}
      ;; Role Information section
      [:> Box
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12] mb-3"}
        "Role information"]
       [forms/input {:label "Name"
                     :placeholder "e.g. read-only"
                     :value (:name role)
                     :required true
                     :on-change #(rf/dispatch [:resource-setup->update-role-name
                                               role-index
                                               (-> % .-target .-value)])}]]
      (when should-show-connection-method?
        [connection-method/main role-index])


      (cond
        (= resource-subtype "ssh")
        [ssh-role-form role-index]

        (= resource-subtype "tcp")
        [tcp-role-form role-index]

        (= resource-subtype "httpproxy")
        [http-proxy-role-form role-index]

        (= resource-subtype "linux-vm")
        [linux-container-role-form role-index]

        (= resource-subtype "kubernetes-token")
        [kubernetes-token-role-form role-index]

        :else
        [metadata-driven-role-form role-index])

      ;; Remove role button (only if more than one role)
      (when can-remove?
        [:> Flex {:justify "end"}
         [:> Button {:size "2"
                     :type "button"
                     :variant "ghost"
                     :color "red"
                     :on-click #(rf/dispatch [:resource-setup->remove-role role-index])}
          [:> Trash2 {:size 16}]
          "Remove Role"]])]]))

;; Main roles step component
(defn main []
  (let [roles @(rf/subscribe [:resource-setup/roles])
        context @(rf/subscribe [:resource-setup/context])]

    [:form {:id "roles-form"
            :on-submit (fn [e]
                         (.preventDefault e)
                         ;; Use different submit event based on context
                         (if (= context :add-role)
                           (rf/dispatch [:add-role->submit])
                           (rf/dispatch [:resource-setup->submit])))}
     [:> Box {:class "p-8 space-y-16"}
      ;; Header
      [:> Box {:class "space-y-2"}
       [:> Heading {:as "h2" :size "6" :weight "bold" :class "text-gray-12"}
        "Setup your Resource roles"]
       [:> Text {:as "p" :size "3" :class "text-gray-12"}
        "Roles are the central concept in Hoop.dev that serve as secure bridges between users and your organization's resources. They enable controlled access to internal services, databases, and other resources while maintaining security and compliance."]
       [:> Text {:as "p" :size "2" :class "text-gray-11 flex items-center gap-1"}
        "Access"
        [:> Flex {:align "center" :gap "1"}
         [:> Link {:href "https://hoop.dev/docs/"
                   :target "_blank"}
          " our Docs"]
         [:> ArrowUpRight {:size 12 :class "text-primary-11"}]]
        " to learn more about Roles."]]

      ;; Render all roles
      (if (empty? roles)
        ;; No roles yet - auto add first one
        (do
          (rf/dispatch [:resource-setup->add-role])
          [:> Box])

        ;; Render existing roles
        [:<>
         (for [role-index (range (count roles))]
           ^{:key role-index}
           [:<>
            [role-configuration role-index]

            ;; Add separator between roles (only between roles, not at the end)
            (when (< role-index (dec (count roles)))
              [:> Box {:class "my-16"}
               [:> Separator {:size "4"}]])])

         ;; Add another role button at the end
         [:> Grid {:columns "7" :gap "7"}
          [:> Box {:grid-column "span 4 / span 4" :grid-column-start "4"}
           [:> Button {:size "2"
                       :variant "soft"
                       :type "button"
                       :on-click #(rf/dispatch [:resource-setup->add-role])}
            [:> Plus {:size 16}]
            "Add New Role"]]]])]]))
