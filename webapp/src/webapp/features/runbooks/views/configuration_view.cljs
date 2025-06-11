(ns webapp.features.runbooks.views.configuration-view
  (:require
   ["@radix-ui/themes" :refer [Box Grid Flex Text Button Heading RadioGroup]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn main [active-tab]
  (let [plugin-details (rf/subscribe [:plugins->plugin-details])
        ;; State atoms
        repository-type (r/atom "public")  ; "public" or "private"
        credential-type (r/atom "http")    ; "http" or "ssh"
        git-url (r/atom "")
        ;; HTTP credentials
        http-user (r/atom "")
        http-token (r/atom "")
        ;; SSH credentials
        ssh-key (r/atom "")
        ssh-user (r/atom "")
        ssh-key-password (r/atom "")
        ssh-known-hosts (r/atom "")
        ;; UI state
        is-submitting (r/atom false)
        config-loaded (r/atom false)

        handle-save (fn []
                      (reset! is-submitting true)
                      (let [config-data (cond-> {:git-url @git-url
                                                 :repository-type @repository-type}
                                          (= @repository-type "private")
                                          (assoc :credential-type @credential-type)

                                          (and (= @repository-type "private")
                                               (= @credential-type "http"))
                                          (assoc :http-user @http-user
                                                 :http-token @http-token)

                                          (and (= @repository-type "private")
                                               (= @credential-type "ssh"))
                                          (assoc :ssh-key @ssh-key
                                                 :ssh-user @ssh-user
                                                 :ssh-key-password @ssh-key-password
                                                 :ssh-known-hosts @ssh-known-hosts))

                            on-success (fn []
                                         ;; Show success message
                                         (rf/dispatch [:show-snackbar
                                                       {:level :success
                                                        :text "Git repository configured!"}])

                                         ;; Reset config-loaded to force reload
                                         (reset! config-loaded false)

                                         ;; Reload plugin data and switch to connections tab
                                         (js/setTimeout
                                          (fn []
                                            (rf/dispatch [:plugins->get-plugin-by-name "runbooks"])
                                            ;; Switch to connections tab after successful save
                                            (js/setTimeout
                                             (fn []
                                               (reset! is-submitting false)
                                               ;; Dispatch event to switch to connections tab
                                               (reset! active-tab "connections"))
                                             200))
                                          200))]

                        ;; Dispatch save with custom success handler
                        (rf/dispatch [:runbooks-plugin->git-config-with-reload config-data on-success])))]

    (rf/dispatch [:plugins->get-plugin-by-name "runbooks"])

    (fn []
      (let [plugin (:plugin @plugin-details)
            config (get-in plugin [:config :envvars])]

        ;; Load existing config and detect repository type
        (when (and (not @config-loaded) config)
          (reset! config-loaded true)

          ;; Load git URL
          (when-let [url (:GIT_URL config)]
            (reset! git-url url))

          ;; Detect repository type and credential type based on existing config
          (let [has-ssh-key? (contains? config :GIT_SSH_KEY)
                has-http-credentials? (or (contains? config :GIT_PASSWORD)
                                          (contains? config :GIT_USER))
                detected-repo-type (if (or has-ssh-key? has-http-credentials?)
                                     "private"
                                     "public")
                detected-cred-type (cond
                                     has-ssh-key? "ssh"
                                     has-http-credentials? "http"
                                     :else "http")] ; default for private repos

            (reset! repository-type detected-repo-type)
            (reset! credential-type detected-cred-type))

          ;; Load HTTP credentials
          (when-let [user (:GIT_USER config)]
            (reset! http-user user))
          (when-let [password (:GIT_PASSWORD config)]
            (reset! http-token password))

          ;; Load SSH credentials
          (when-let [key (:GIT_SSH_KEY config)]
            (reset! ssh-key key))
          (when-let [user (:GIT_SSH_USER config)]
            (reset! ssh-user user))
          (when-let [keypass (:GIT_SSH_KEYPASS config)]
            (reset! ssh-key-password keypass))
          (when-let [hosts (:GIT_SSH_KNOWN_HOSTS config)]
            (reset! ssh-known-hosts hosts)))

        [:> Box {:py "7" :class "space-y-radix-9"}
         ;; Repository Privacy Type Section
         [:> Grid {:columns "7" :gap "7"}
          [:> Box {:grid-column "span 2 / span 2"}
           [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
            "Repository privacy type"]
           [:> Text {:size "3" :class "text-[--gray-11]"}
            "Select the type of Git repository to connect."]]

          [:> Box {:class "space-y-radix-4" :grid-column "span 5 / span 5"}
           [:> Flex {:asChild true :gap "4" :direction "column"}
            [:> RadioGroup.Root {:onValueChange #(reset! repository-type %)
                                 :value @repository-type}
             [:> RadioGroup.Item {:value "public"}
              [:> Text {:size "3"} "Public"]]
             [:> RadioGroup.Item {:value "private"}
              [:> Text {:size "3"} "Private"]]]]]]

         ;; Repository Credentials Section
         [:> Grid {:columns "7" :gap "7"}
          [:> Box {:grid-column "span 2 / span 2"}
           [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
            "Repository credentials"]
           [:> Text {:size "3" :class "text-[--gray-11]"}
            "Provide repository access information."]

           ;; Learn more button
           [:> Box {:mt "4"}
            [:> Button {:variant "ghost" :size "2" :class "text-[--gray-11] p-0"}
             "↗ Learn more about Runbooks"]]]

          [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
           ;; Credential type selection (only for private repos)
           (when (= @repository-type "private")
             [:> Flex {:asChild true :gap "4" :direction "column"}
              [:> RadioGroup.Root {:onValueChange #(reset! credential-type %)
                                   :value @credential-type}
               [:> RadioGroup.Item {:value "http"}
                [:> Text {:size "3"} "HTTP"]]
               [:> RadioGroup.Item {:value "ssh"}
                [:> Text {:size "3"} "SSH"]]]])

           ;; Git URL (always visible)
           [forms/input {:label "Git URL"
                         :placeholder "git@github.com:mycompany/runbooks.git"
                         :value @git-url
                         :on-change #(reset! git-url (-> % .-target .-value))
                         :class "w-full"}]

           ;; HTTP credentials (for private HTTP repos)
           (when (and (= @repository-type "private")
                      (= @credential-type "http"))
             [:<>
              [forms/input {:label "User (Optional)"
                            :placeholder "············"
                            :value @http-user
                            :on-change #(reset! http-user (-> % .-target .-value))
                            :class "w-full"}]
              [:> Text {:size "2" :class "text-[--gray-11] -mt-2 mb-4"}
               "Defaults to 'oauth2'. Override only for custom Git providers that require specific usernames."]

              [forms/input {:label "Access Password or Token"
                            :type "password"
                            :placeholder "············"
                            :value @http-token
                            :on-change #(reset! http-token (-> % .-target .-value))
                            :class "w-full"}]])

           ;; SSH credentials (for private SSH repos)
           (when (and (= @repository-type "private")
                      (= @credential-type "ssh"))
             [:<>
              [forms/textarea {:label "SSH Key"
                               :placeholder "············"
                               :value @ssh-key
                               :on-change #(reset! ssh-key (-> % .-target .-value))
                               :class "w-full"
                               :rows 6}]

              [forms/input {:label "SSH User (Optional)"
                            :placeholder "············"
                            :value @ssh-user
                            :on-change #(reset! ssh-user (-> % .-target .-value))
                            :class "w-full"}]

              [forms/input {:label "SSH Key Password"
                            :type "password"
                            :placeholder "············"
                            :value @ssh-key-password
                            :on-change #(reset! ssh-key-password (-> % .-target .-value))
                            :class "w-full"}]

              [forms/textarea {:label "SSH Known Hosts File (Optional)"
                               :placeholder "hostname[,ip] key-type public-key [comment]"
                               :value @ssh-known-hosts
                               :on-change #(reset! ssh-known-hosts (-> % .-target .-value))
                               :class "w-full"
                               :rows 4}]])]]

         ;; Save button
         [:> Flex {:justify "end" :class "w-full"}
          [:> Button {:size "4"
                      :loading @is-submitting
                      :disabled @is-submitting
                      :on-click handle-save}
           "Save"]]]))))
