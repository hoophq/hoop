(ns webapp.resources.views.setup.roles-step
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Link Separator
                               Text]]
   ["lucide-react" :refer [ArrowUpRight Plus Trash2]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.resources.constants :as constants]
   [webapp.resources.views.setup.configuration-inputs :as configuration-inputs]))

;; SSH role form - Based on server.cljs
(defn ssh-role-form [role-index]
  (let [configs (constants/get-role-config "application" "ssh")
        credentials @(rf/subscribe [:resource-setup/role-credentials role-index])
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
        (let [base-props {:label (:label field)
                          :placeholder (or (:placeholder field) (str "e.g. " (:key field)))
                          :value (get credentials (:key field) "")
                          :required (:required field)
                          :on-change #(rf/dispatch [:resource-setup->update-role-credentials
                                                    role-index
                                                    (:key field)
                                                    (-> % .-target .-value)])}]
          (if (= (:type field) "textarea")
            [forms/textarea base-props]
            [forms/input (assoc base-props :type "password")])))]]))

;; TCP role form - Based on network.cljs
(defn tcp-role-form [role-index]
  (let [configs (constants/get-role-config "application" "tcp")
        credentials @(rf/subscribe [:resource-setup/role-credentials role-index])]
    [:> Grid {:columns "1" :gap "4"}
     (for [field configs]
       ^{:key (:key field)}
       [forms/input {:label (:label field)
                     :placeholder (or (:placeholder field) (str "e.g. " (:key field)))
                     :value (get credentials (:key field) "")
                     :required (:required field)
                     :type "password"  ;; Credenciais sensíveis
                     :on-change #(rf/dispatch [:resource-setup->update-role-credentials
                                               role-index
                                               (:key field)
                                               (-> % .-target .-value)])}])]))

;; HTTP Proxy role form - Based on network.cljs
(defn http-proxy-role-form [role-index]
  (let [credentials @(rf/subscribe [:resource-setup/role-credentials role-index])]
    [:> Box {:class "space-y-4"}
     ;; Remote URL
     [forms/input {:label "Remote URL"
                   :placeholder "e.g. http://example.com"
                   :value (get credentials "remote_url" "")
                   :required true
                   :type "text"
                   :on-change #(rf/dispatch [:resource-setup->update-role-credentials
                                             role-index
                                             "remote_url"
                                             (-> % .-target .-value)])}]

     ;; HTTP headers section (usando configuration-inputs)
     [configuration-inputs/environment-variables-section role-index
      {:title "HTTP headers"
       :subtitle "Add HTTP headers that will be used in your requests."}]]))

;; Custom/Metadata-driven role form (includes databases)
(defn metadata-driven-role-form [role-index]
  (let [subtype @(rf/subscribe [:resource-setup/resource-subtype])
        connections-metadata @(rf/subscribe [:connections->metadata])
        connection (when connections-metadata
                     (->> (:connections connections-metadata)
                          (filter #(= (get-in % [:resourceConfiguration :subtype]) subtype))
                          first))
        credentials-config (get-in connection [:resourceConfiguration :credentials])
        metadata-credentials @(rf/subscribe [:resource-setup/metadata-credentials role-index])]

    (when (seq credentials-config)
      [:> Grid {:columns "1" :gap "4"}
       (for [field credentials-config]
         (let [sanitized-name (cs/capitalize
                               (cs/lower-case
                                (cs/replace (:name field) #"[^a-zA-Z0-9]" " ")))
               env-var-name (:name field)
               field-type (case (:type field)
                            "filesystem" "textarea"
                            "textarea" "textarea"
                            "password")]
           (if (= field-type "textarea")
             ^{:key env-var-name}
             [forms/textarea {:label sanitized-name
                              :placeholder (or (:placeholder field) (:description field))
                              :value (get metadata-credentials env-var-name "")
                              :required (:required field)
                              :helper-text (:description field)
                              :on-change #(rf/dispatch [:resource-setup->update-role-metadata-credentials
                                                        role-index
                                                        env-var-name
                                                        (-> % .-target .-value)])}]
             ^{:key env-var-name}
             [forms/input {:label sanitized-name
                           :placeholder (or (:placeholder field) (:description field))
                           :value (get metadata-credentials env-var-name "")
                           :required (:required field)
                           :type field-type
                           :helper-text (:description field)
                           :on-change #(rf/dispatch [:resource-setup->update-role-metadata-credentials
                                                     role-index
                                                     env-var-name
                                                     (-> % .-target .-value)])}])))])))

;; Linux/Container role form - Based on server.cljs
(defn linux-container-role-form [role-index]
  [:> Box {:class "space-y-6"}
   ;; Environment variables section (usando configuration-inputs)
   [configuration-inputs/environment-variables-section role-index {}]

   ;; Configuration files section (usando configuration-inputs)
   [configuration-inputs/configuration-files-section role-index]

   ;; Additional command section
   [:> Box {:class "space-y-3"}
    [:> Heading {:as "h4" :size "3" :weight "medium"}
     "Additional command"]
    [:> Text {:size "2" :class "text-[--gray-11]"}
     "Add an additional command that will run on your resource role. Variables (like the ones above) can also be used here."]
    [forms/input {:label "Command"
                  :placeholder "$ bash"
                  :value ""
                  :on-change #()}]]])

;; Single role configuration
(defn role-configuration [role-index]
  (let [roles @(rf/subscribe [:resource-setup/roles])
        role (get roles role-index)
        resource-type @(rf/subscribe [:resource-setup/resource-type])
        resource-subtype @(rf/subscribe [:resource-setup/resource-subtype])
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

      ;; Role credentials section
      [:> Box
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12] mb-3"}
        "Role credentials"]
       ;; Render appropriate form based on type
       (cond
         (= resource-subtype "ssh")
         [ssh-role-form role-index]

         (= resource-subtype "tcp")
         [tcp-role-form role-index]

         (= resource-subtype "httpproxy")
         [http-proxy-role-form role-index]

         (= resource-subtype "linux-vm")
         [linux-container-role-form role-index]

         :else
         [metadata-driven-role-form role-index])]

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
