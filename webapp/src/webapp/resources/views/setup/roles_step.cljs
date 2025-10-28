(ns webapp.resources.views.setup.roles-step
  (:require
   ["@radix-ui/themes" :refer [Box Heading Link Text Button Flex Grid Separator]]
   ["lucide-react" :refer [Trash2 Plus ArrowUpRight]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.resources.constants :as constants]))

;; Database role form
(defn database-role-form [role-index]
  (let [subtype @(rf/subscribe [:resource-setup/resource-subtype])
        configs (constants/get-role-config "database" subtype)
        credentials @(rf/subscribe [:resource-setup/role-credentials role-index])]
    (when configs
      [:> Grid {:columns "1" :gap "4"}
       (for [field configs]
         ^{:key (:key field)}
         (let [field-type (or (:type field) "password")]
           [forms/input {:label (:label field)
                         :placeholder (or (:placeholder field) (str "e.g. " (:key field)))
                         :value (get credentials (:key field) "")
                         :required (:required field)
                         :type field-type
                         :on-change #(rf/dispatch [:resource-setup->update-role-credentials
                                                   role-index
                                                   (:key field)
                                                   (-> % .-target .-value)])}]))])))

;; SSH role form
(defn ssh-role-form [role-index]
  (let [configs (constants/get-role-config "application" "ssh")
        credentials @(rf/subscribe [:resource-setup/role-credentials role-index])]
    [:> Box {:class "space-y-4"}
     [:> Grid {:columns "1" :gap "4"}
      (for [field configs]
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

;; TCP role form
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
                     :type "text"
                     :on-change #(rf/dispatch [:resource-setup->update-role-credentials
                                               role-index
                                               (:key field)
                                               (-> % .-target .-value)])}])]))

;; HTTP Proxy role form
(defn http-proxy-role-form [role-index]
  (let [credentials @(rf/subscribe [:resource-setup/role-credentials role-index])]
    [:> Box {:class "space-y-4"}
     [forms/input {:label "Remote URL"
                   :placeholder "e.g. http://example.com"
                   :value (get credentials "remote_url" "")
                   :required true
                   :type "text"
                   :on-change #(rf/dispatch [:resource-setup->update-role-credentials
                                             role-index
                                             "remote_url"
                                             (-> % .-target .-value)])}]

     [:> Box
      [:> Text {:size "2" :weight "bold" :class "text-[--gray-12] mb-2"}
       "HTTP headers"]
      [:> Button {:size "2"
                  :variant "soft"
                  :on-click #(rf/dispatch [:resource-setup->add-role-env-var role-index "" ""])}
       [:> Plus {:size 16}]
       "Add key/value"]]]))

;; Custom/Metadata-driven role form
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
      [:> Box {:class "space-y-4"}
       [:> Heading {:as "h3" :size "4" :weight "bold"}
        "Environment credentials"]

       [:> Grid {:columns "1" :gap "4"}
        (for [field credentials-config]
          (let [form-key (cs/lower-case (cs/replace (:name field) #"[^a-zA-Z0-9]" ""))
                field-type (case (:type field)
                             "filesystem" "textarea"
                             "textarea" "textarea"
                             "password")]
            ^{:key form-key}
            (if (= field-type "textarea")
              [forms/textarea {:label (:name field)
                               :placeholder (or (:placeholder field) (:description field))
                               :value (get metadata-credentials form-key "")
                               :required (:required field)
                               :helper-text (:description field)
                               :on-change #(rf/dispatch [:resource-setup->update-role-metadata-credentials
                                                         role-index
                                                         form-key
                                                         (-> % .-target .-value)])}]
              [forms/input {:label (:name field)
                            :placeholder (or (:placeholder field) (:description field))
                            :value (get metadata-credentials form-key "")
                            :required (:required field)
                            :type field-type
                            :helper-text (:description field)
                            :on-change #(rf/dispatch [:resource-setup->update-role-metadata-credentials
                                                      role-index
                                                      form-key
                                                      (-> % .-target .-value)])}])))]

       ;; Environment variables section
       [:> Box {:class "mt-6"}
        [:> Heading {:as "h4" :size "3" :weight "bold" :class "mb-3"}
         "Environment variables"]
        [:> Text {:size "2" :class "text-[--gray-11] mb-3"}
         "Include environment variables to be used in your connection."]
        [:> Button {:size "2"
                    :variant "soft"
                    :on-click #(rf/dispatch [:resource-setup->add-role-env-var role-index "" ""])}
         [:> Plus {:size 16}]
         "Add key/value"]]

       ;; Configuration files section
       [:> Box {:class "mt-6"}
        [:> Heading {:as "h4" :size "3" :weight "bold" :class "mb-3"}
         "Configuration files"]
        [:> Text {:size "2" :class "text-[--gray-11] mb-3"}
         "Add values from your configuration file and use them as an environment variable in your connection."]
        [:> Button {:size "2"
                    :variant "soft"
                    :on-click #(rf/dispatch [:resource-setup->add-role-config-file role-index "" ""])}
         [:> Plus {:size 16}]
         "Add"]]])))

;; Linux/Container role form
(defn linux-container-role-form [role-index]
  [:> Box {:class "space-y-4"}
   ;; Environment variables
   [:> Box
    [:> Heading {:as "h4" :size "3" :weight "bold" :class "mb-3"}
     "Environment variables"]
    [:> Text {:size "2" :class "text-[--gray-11] mb-3"}
     "Include environment variables to be used in your connection."]
    [:> Button {:size "2"
                :variant "soft"
                :on-click #(rf/dispatch [:resource-setup->add-role-env-var role-index "" ""])}
     [:> Plus {:size 16}]
     "Add key/value"]]

   ;; Configuration files
   [:> Box {:class "mt-6"}
    [:> Heading {:as "h4" :size "3" :weight "bold" :class "mb-3"}
     "Configuration files"]
    [:> Text {:size "2" :class "text-[--gray-11] mb-3"}
     "Add values from your configuration file and use them as an environment variable in your connection."]
    [:> Button {:size "2"
                :variant "soft"
                :on-click #(rf/dispatch [:resource-setup->add-role-config-file role-index "" ""])}
     [:> Plus {:size 16}]
     "Add"]]

   ;; Additional command
   [:> Box {:class "mt-6"}
    [:> Heading {:as "h4" :size "3" :weight "bold" :class "mb-3"}
     "Additional command"]
    [:> Text {:size "2" :class "text-[--gray-11] mb-3"}
     "Add an additional command that will run on your connection. Variables (like the ones above) can also be used here."]
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
         (= resource-type "database")
         [database-role-form role-index]

         (= resource-subtype "ssh")
         [ssh-role-form role-index]

         (= resource-subtype "tcp")
         [tcp-role-form role-index]

         (= resource-subtype "httpproxy")
         [http-proxy-role-form role-index]

         (= resource-type "custom")
         (if (= resource-subtype "linux-vm")
           [linux-container-role-form role-index]
           [metadata-driven-role-form role-index])

         :else
         [linux-container-role-form role-index])]

      ;; Remove role button (only if more than one role)
      (when can-remove?
        [:> Flex {:justify "end"}
         [:> Button {:size "2"
                     :variant "ghost"
                     :color "red"
                     :on-click #(rf/dispatch [:resource-setup->remove-role role-index])}
          [:> Trash2 {:size 16}]
          "Remove Role"]])]]))

;; Main roles step component
(defn main []
  (let [roles @(rf/subscribe [:resource-setup/roles])]

    [:form {:id "roles-form"
            :on-submit (fn [e]
                         (.preventDefault e)
                         (rf/dispatch [:resource-setup->submit]))}
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
